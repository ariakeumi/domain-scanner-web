package worker

import (
	"context"
	"time"

	"domain_scanner/internal/domain"
	"domain_scanner/internal/stats"
	"domain_scanner/internal/types"
)

// Worker 域名检查工作协程
func Worker(id int, jobs <-chan string, results chan<- types.DomainResult, delay time.Duration, collector *stats.Collector) {
	WorkerWithContext(context.Background(), id, jobs, results, delay, collector, nil)
}

// WorkerWithContext 域名检查工作协程，支持取消。
// slots 是全局 worker 信号量（可为 nil，nil 表示不限制），用于限制所有扫描任务
// 的并发检查总数；获取信号量时也会响应 ctx 取消。
func WorkerWithContext(ctx context.Context, id int, jobs <-chan string, results chan<- types.DomainResult, delay time.Duration, collector *stats.Collector, slots chan struct{}) {
	for {
		var domainName string
		select {
		case <-ctx.Done():
			return
		case job, ok := <-jobs:
			if !ok {
				return
			}
			domainName = job
		}

		// 获取全局并发额度（等待时也响应取消）
		if slots != nil {
			select {
			case slots <- struct{}{}:
			case <-ctx.Done():
				return
			}
		}

		// 标记 Worker 开始工作
		if collector != nil {
			collector.IncrementActiveWorkers()
		}

		available, err := domain.CheckDomainAvailability(domainName)
		signatures, _ := domain.CheckDomainSignatures(domainName)

		results <- types.DomainResult{
			Domain:     domainName,
			Available:  available,
			Error:      err,
			Signatures: signatures,
		}

		// 标记 Worker 完成当前任务
		if collector != nil {
			collector.DecrementActiveWorkers()
		}

		// 释放全局并发额度
		if slots != nil {
			<-slots
		}

		if delay <= 0 {
			continue
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
	}
}
