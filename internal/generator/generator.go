package generator

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/dlclark/regexp2"
)

// DomainGenerator 包含生成的域名和计数信息
type DomainGenerator struct {
	Domains    <-chan string
	TotalCount int
	Generated  *int64 // 用atomic操作的计数器
}

// GenerateDomains 返回一个包含域名和计数信息的结构体
func GenerateDomains(length int, suffix string, pattern string, regexFilter string, dictFile string) (*DomainGenerator, error) {
	return GenerateDomainsWithContext(context.Background(), length, suffix, pattern, regexFilter, dictFile)
}

// GenerateDomainsWithContext 返回一个支持取消的域名生成器
func GenerateDomainsWithContext(ctx context.Context, length int, suffix string, pattern string, regexFilter string, dictFile string) (*DomainGenerator, error) {
	letters := "abcdefghijklmnopqrstuvwxyz"
	numbers := "0123456789"

	var regex *regexp2.Regexp
	var err error
	if regexFilter != "" {
		// 验证正则表达式复杂度
		if err := validateRegexComplexity(regexFilter); err != nil {
			return nil, fmt.Errorf("regex pattern rejected: %w", err)
		}

		regex, err = regexp2.Compile(regexFilter, regexp2.None)
		if err != nil {
			return nil, fmt.Errorf("invalid regex pattern: %w", err)
		}

		// 设置超时保护防止 ReDoS 攻击
		regex.MatchTimeout = 100 * time.Millisecond
	}

	domainChan := make(chan string, 1000) // 缓冲池以提高性能
	var generated int64 = 0
	var totalEstimated int

	// 字典模式或传统模式的预估计算
	var words []string
	if dictFile != "" {
		// 字典模式：计算字典中的单词数量
		words, err = readDictionaryFile(dictFile)
		if err != nil {
			return nil, fmt.Errorf("error reading dictionary file: %w", err)
		}
		totalEstimated = len(words)
	} else {
		if length <= 0 {
			return nil, fmt.Errorf("domain length must be greater than 0")
		}

		// 传统模式：计算组合数量
		var charsetSize int
		switch pattern {
		case "d":
			charsetSize = len(numbers)
		case "D":
			charsetSize = len(letters)
		case "a":
			charsetSize = len(letters + numbers)
		default:
			return nil, fmt.Errorf("invalid pattern %q, use d, D, or a", pattern)
		}

		totalEstimated = 1
		for i := 0; i < length; i++ {
			totalEstimated *= charsetSize
		}
	}

	go func() {
		defer close(domainChan)

		if dictFile != "" {
			// 字典模式：从文件读取单词
			generateFromWords(ctx, domainChan, words, suffix, regex, &generated)
		} else {
			// 传统模式：生成字符组合
			switch pattern {
			case "d":
				generateCombinationsIterative(ctx, domainChan, numbers, length, suffix, regex, &generated)
			case "D":
				generateCombinationsIterative(ctx, domainChan, letters, length, suffix, regex, &generated)
			case "a":
				generateCombinationsIterative(ctx, domainChan, letters+numbers, length, suffix, regex, &generated)
			}
		}
	}()

	return &DomainGenerator{
		Domains:    domainChan,
		TotalCount: totalEstimated,
		Generated:  &generated,
	}, nil
}

// GenerateDomainsFromWordsWithContext 使用字典单词创建支持取消的域名生成器
func GenerateDomainsFromWordsWithContext(ctx context.Context, words []string, suffix string, regexFilter string) (*DomainGenerator, error) {
	var regex *regexp2.Regexp
	var err error
	if regexFilter != "" {
		if err := validateRegexComplexity(regexFilter); err != nil {
			return nil, fmt.Errorf("regex pattern rejected: %w", err)
		}

		regex, err = regexp2.Compile(regexFilter, regexp2.None)
		if err != nil {
			return nil, fmt.Errorf("invalid regex pattern: %w", err)
		}
		regex.MatchTimeout = 100 * time.Millisecond
	}

	cleanWords := normalizeWords(words)
	if len(cleanWords) == 0 {
		return nil, fmt.Errorf("dictionary is empty or contains no valid words")
	}

	domainChan := make(chan string, 1000)
	var generated int64

	go func() {
		defer close(domainChan)
		generateFromWords(ctx, domainChan, cleanWords, suffix, regex, &generated)
	}()

	return &DomainGenerator{
		Domains:    domainChan,
		TotalCount: len(cleanWords),
		Generated:  &generated,
	}, nil
}

// generateCombinationsIterative 使用迭代方法而非递归方法防止堆栈溢出
func generateCombinationsIterative(ctx context.Context, domainChan chan<- string, charset string, length int, suffix string, regex *regexp2.Regexp, generated *int64) {
	charsetSize := len(charset)
	if charsetSize == 0 || length <= 0 {
		return
	}

	// 使用计数器方法生成组合
	total := 1
	for i := 0; i < length; i++ {
		total *= charsetSize
	}

	for counter := 0; counter < total; counter++ {
		select {
		case <-ctx.Done():
			return
		default:
		}

		current := ""
		temp := counter

		// 从计数器生成域名字符串
		for i := 0; i < length; i++ {
			current = string(charset[temp%charsetSize]) + current
			temp /= charsetSize
		}

		domain := current + suffix

		// 正则过滤（只对域名前缀进行匹配）
		var match bool
		if regex == nil {
			match = true
		} else {
			var err error
			match, err = safeRegexMatch(regex, current)
			if err != nil {
				// 正则匹配错误时跳过该域名
				match = false
			}
		}

		if match {
			select {
			case <-ctx.Done():
				return
			case domainChan <- domain:
			}
			// 使用atomic操作增加计数器
			atomic.AddInt64(generated, 1)
		}
	}
}

// validateRegexComplexity 检查正则表达式的复杂度，防止潜在的 ReDoS 攻击
func validateRegexComplexity(pattern string) error {
	// 检查长度限制
	if len(pattern) > 200 {
		return fmt.Errorf("regex pattern too long (max 200 characters)")
	}

	// 检查已知危险模式
	dangerousPatterns := []string{
		"(.*)*",       // 嵌套量词
		"(.+)+",       // 嵌套量词
		"(a+)+",       // 经典 ReDoS 模式
		"(a*)*",       // 嵌套星号
		"(.{0,})*",    // 复杂嵌套
		"(\\w+)*\\w*", // 复杂单词匹配
	}

	for _, dangerous := range dangerousPatterns {
		if strings.Contains(pattern, dangerous) {
			return fmt.Errorf("detected potentially dangerous regex pattern: %s", dangerous)
		}
	}

	// 检查嵌套量词数量
	nestedCount := strings.Count(pattern, "+") + strings.Count(pattern, "*")
	if nestedCount > 5 {
		return fmt.Errorf("too many quantifiers in regex pattern (max 5)")
	}

	return nil
}

// safeRegexMatch 安全地执行正则表达式匹配，包含超时和错误处理
func safeRegexMatch(regex *regexp2.Regexp, input string) (bool, error) {
	if regex == nil {
		return true, nil
	}

	// 确保超时已设置
	if regex.MatchTimeout == 0 {
		regex.MatchTimeout = 100 * time.Millisecond
	}

	match, err := regex.MatchString(input)
	if err != nil {
		return false, fmt.Errorf("regex matching failed for pattern '%s' with input '%s': %w", regex.String(), input, err)
	}

	return match, nil
}

// readDictionaryFile 读取字典文件并返回单词列表
func readDictionaryFile(dictFile string) ([]string, error) {
	file, err := os.Open(dictFile)
	if err != nil {
		return nil, fmt.Errorf("failed to open dictionary file: %w", err)
	}
	defer file.Close()

	var words []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		word := strings.TrimSpace(scanner.Text())
		if word != "" && !strings.Contains(word, " ") {
			words = append(words, word)
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading dictionary file: %w", err)
	}

	words = normalizeWords(words)

	if len(words) == 0 {
		return nil, fmt.Errorf("dictionary file is empty or contains no valid words")
	}

	return words, nil
}

func normalizeWords(words []string) []string {
	normalized := make([]string, 0, len(words))
	for _, word := range words {
		word = strings.TrimSpace(word)
		if word != "" && !strings.Contains(word, " ") {
			normalized = append(normalized, word)
		}
	}
	return normalized
}

// generateFromWords 从字典单词生成域名
func generateFromWords(ctx context.Context, domainChan chan<- string, words []string, suffix string, regex *regexp2.Regexp, generated *int64) {
	for _, word := range words {
		select {
		case <-ctx.Done():
			return
		default:
		}

		domain := word + suffix

		// 正则过滤（只对域名前缀进行匹配）
		var match bool
		if regex == nil {
			match = true
		} else {
			var err error
			match, err = safeRegexMatch(regex, word)
			if err != nil {
				// 正则匹配错误时跳过该域名
				match = false
			}
		}

		if match {
			select {
			case <-ctx.Done():
				return
			case domainChan <- domain:
			}
			// 使用atomic操作增加计数器
			atomic.AddInt64(generated, 1)
		}
	}
}
