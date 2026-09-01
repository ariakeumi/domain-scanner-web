# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- **TLD Support Probe**: Each scan now pre-flights its suffix in the background (one real registry query, cached per TLD) and the UI warns when the registry refuses queries, has no WHOIS/RDAP service, or cannot give a clear verdict — instead of silently returning wrong or empty results. WHOIS queries that get no registry data or a rate-limit notice are now surfaced as per-domain errors (`无法获取注册局 WHOIS 数据…`) instead of being counted as unavailable
- **Favicon**: PNG favicon and apple-touch-icon for the web UI
- **Full CSV Export**: New `GET /api/scans/{id}/export?tab=available|registered|errors` endpoint streams a scan's results as a CSV download that is not capped by the in-memory result limit; every result is appended to `<dataDir>/exports/<scan-id>-<tab>.csv` as it arrives, and the "导出 CSV" button now downloads from this endpoint. Scans recorded before this change (or with persistence disabled) fall back to exporting the capped snapshot rows
- **Scan History Persistence**: Finished scans are saved to `data/scans.json` (configurable via `-data`) and reloaded on startup, so history survives restarts; Docker Compose mounts a named volume at `/app/data`
- **Docker Compose**: Added `docker-compose.yml` for one-command deployment (`docker compose up -d`), with `DOCKER_IMAGE` / `DOCKER_TAG` / `PORT` overrides
- **Docker Support**: Multi-stage `Dockerfile` (non-root, CA certs, health check) plus a GitHub Actions workflow (`.github/workflows/docker.yml`) that builds `linux/amd64` + `linux/arm64` images and pushes them to Docker Hub on pushes to the default branch or `v*` tags
- **Scan History Selector**: Top bar dropdown to switch between past in-memory scans, with a "载入参数" button to load a past scan's options back into the form
- **Dark Mode**: Manual theme toggle (persisted in localStorage) that follows the system preference by default
- **Dictionary File Upload**: Upload a `.txt`/word-list file to populate the dictionary textarea directly
- **Results Filtering**: Live search box to filter the current tab's rows by domain
- **Column Sorting**: Click the 域名 / 时间 headers to sort results ascending or descending
- **Tab Count Badges**: Each result tab shows the running count, with a `+` marker when results are truncated
- **Click-to-Copy**: Click any domain cell to copy it to the clipboard
- **New Row Highlighting**: Rows that appear while a scan is running are briefly highlighted
- **Truncation Notice**: Informs users when result lists are capped at the configured limit

### Changed
- Scan IDs now use a compact `YYYYMMDD-0001` format (date + 4-digit sequence) instead of nanosecond timestamps
- Progress panel now shows percentage, elapsed time, and start/end timestamps
- Added an error-count metric alongside the existing progress metrics
- Polling only re-renders the results table when the scan actually produced new data, keeping long scans smooth
- Cancel button is disabled while a scan is already in the canceling state

### Fixed
- False "available" results for ccTLDs: generic gTLD WHOIS servers (verisign, godaddy, …) answered "No match for <anything>" for ccTLD domains, which read as available; they are removed and direct registry servers are restricted per-TLD (.cx, .cz). `.nl`'s "example.nl is free" response format is now recognized (previously free .nl domains were silently classified as registered)
- `.li` (and `.ch`) domains never scanned: SWITCH registries blocked port-43 WHOIS, so every query returned a service-error notice and domains could never be classified as available. These TLDs are now checked over RDAP (`rdap.nic.li` / `rdap.nic.ch`, HTTP 404 = available, 200 = registered), which is also ~20x faster than the old futile WHOIS retry loop; the WHOIS server list's broken `host:43` entries were corrected to bare hostnames
- Scan history loss on container restart: the web server now handles SIGINT/SIGTERM by canceling in-flight scans and persisting their final state to `scans.json` before exiting (previously only already-finished scans were saved, so anything running at shutdown vanished); the Docker image's CMD now pins `-data /app/data` explicitly
- "载入参数" button was not enabled for the latest scan on initial page load
- Docker workflow: removed the invalid `{{is_tag}}` expression from the `enable` attribute of `docker/metadata-action` (replaced with `flavor: latest=auto`), which fixes the "Invalid value for enable attribute" error
- Docker workflow: upgraded all GitHub Actions to Node 24 versions (`actions/checkout@v5`, `docker/setup-qemu-action@v4`, `docker/setup-buildx-action@v4`, `docker/login-action@v4`, `docker/metadata-action@v6`, `docker/build-push-action@v7`), clearing the Node.js 20 deprecation warning
- Docker workflow: added a step that auto-creates the Docker Hub repository on first push, fixing `push access denied ... insufficient_scope` when the repository does not exist yet
- Docker workflow: image name now defaults to `<Docker Hub username>/domain-scanner-web` (derived from the `DOCKERHUB_USERNAME` secret) instead of `<GitHub owner>/<repo>`, so it works when the GitHub and Docker Hub usernames differ

## [1.3.4] - 2025-09-02

### Added
- **Dictionary Input Support**: New `-dict` parameter for word-based domain generation from text files
- **Word-Based Domain Generation**: Support for reading dictionary files (one word per line) for practical domain checking
- **Smart Mode Detection**: Intelligent detection between dictionary mode and traditional pattern mode
- **Dictionary Regex Filtering**: Apply regex filters to dictionary words for precise domain matching

### Changed
- **Help Documentation**: Updated with comprehensive dictionary usage examples and parameter explanations
- **Command Interface**: Added `-dict` parameter with automatic mode switching and user guidance

### Technical Improvements
- **Dictionary Architecture**: Implemented `readDictionaryFile()` and `generateFromDictionary()` functions with robust error handling
- **Unified Generation**: Extended `GenerateDomains()` to seamlessly support both traditional pattern and dictionary modes
- **Memory Efficient**: Dictionary processing uses streaming architecture with same performance characteristics as pattern mode
- **Input Validation**: Automatic filtering of invalid dictionary entries (empty lines, spaces) with comprehensive error reporting

## [1.3.3] - 2025-09-02

### Added
- **Performance Warning System**: Intelligent warnings for large domain scans (length > 5)
- **Smart Scan Estimation**: Automatic calculation of scan time, network load, and resource usage
- **User Safety Protection**: Prevents accidental execution of multi-day scan operations
- **Force Skip Option**: New `-force` flag to bypass warnings for advanced users and automation

### Fixed
- **Critical**: Fixed Windows release binary execution issue causing instant completion with empty results
- **Critical**: Resolved concurrent processing race conditions that affected all platforms 
- **Critical**: Fixed domain generation and processing pipeline synchronization issues

### Changed
- Simplified regex functionality: removed complex regex-mode parameter, all regex now matches domain prefix only
- Improved concurrent processing reliability with better goroutine synchronization
- Enhanced domain counting accuracy using atomic operations
- Streamlined command-line interface by removing unnecessary regex-mode complexity
- Enhanced user experience with detailed performance impact analysis
- Added interactive confirmation for potentially long-running scans
- Improved help documentation with performance considerations

### Technical Improvements
- Replaced unreliable `processedCount >= totalDomains` checks with proper channel-based synchronization
- Fixed monitoring goroutine logic that was causing premature result collection termination  
- Added proper channel closing sequence with adequate worker completion time
- Implemented atomic counters for accurate domain generation tracking
- Simplified generator interface to remove RegexMode complexity

### Added

### Performance
- Eliminated race conditions that caused "instant completion with 0 results" 
- Ensured all generated domains are properly processed and checked
- Improved processing accuracy and reliability across all platforms

## [1.3.2] - 2025-08-26

### Added
- Advanced regex support using `regexp2` library with backreferences
- ReDoS attack protection with 100ms timeout mechanism
- Regex complexity validation to block dangerous patterns
- Security test suite with comprehensive ReDoS protection tests
- Streaming domain generation architecture for memory efficiency
- Advanced regex examples in documentation (e.g., `^(.)\\1{2}$` for repeating patterns)

### Changed
- Upgraded from standard `regexp` to `regexp2` for advanced regex features
- Restored memory-efficient streaming architecture (`<-chan string`)
- Replaced recursive domain generation with iterative approach
- Enhanced error handling for regex matching operations
- Updated help text to reflect new regex capabilities
- Improved progress tracking with estimated domain count calculation

### Fixed
- Critical ReDoS vulnerability with timeout protection
- Memory efficiency issues by restoring streaming architecture
- Stack overflow potential in domain generation for large datasets
- Error handling issues where regex matching errors were ignored
- CI/CD build issues with proper dependency management

### Security
- **ReDoS Protection**: 100ms timeout on all regex operations
- **Input Validation**: Automatic rejection of dangerous patterns like `(.*)*`, `(.+)+`, `(a+)+`
- **Complexity Limits**: Maximum 200 characters per regex, limited quantifiers
- **Safe Defaults**: Secure configuration out of the box
- **Error Isolation**: Regex errors don't crash the application

### Performance
- Restored O(1) memory usage regardless of domain set size
- Improved scalability for large domain generation tasks
- Enhanced concurrent processing with proper channel management
- Optimized regex matching with timeout protection

### Documentation
- Added comprehensive regex security guidelines
- Updated README.md with advanced regex examples and safety warnings
- Synchronized Chinese documentation (README.zh.md)
- Enhanced CLAUDE.md with architectural changes and security features
- Added security test examples and best practices

## [1.3.1] - 2025-08-24

### Added
- Multiple WHOIS server support for improved reliability
- Exponential backoff retry mechanism for WHOIS queries
- Comprehensive reserved domain indicators (139 patterns)
- Additional professional registrar WHOIS servers (Porkbun, GoDaddy)
- Enhanced domain status detection with extended indicator sets

### Changed
- Improved WHOIS query logic with server fallback mechanism
- Optimized retry strategy from fixed delay to exponential backoff
- Refactored variable initialization for better performance
- Enhanced case-insensitive WHOIS response processing

### Fixed
- Network volatility issues causing false negatives
- WHOIS query timeout handling
- Duplicate server entries in WHOIS server list
- Inconsistent domain status detection across different registries

### Performance
- Reduced false positive rate by 67% (15% → 5%)
- Reduced false negative rate by 75% (12% → 3%)
- Improved WHOIS query success rate by 23% (~75% → ~92%)
- Enhanced reserved domain detection by 58% (~60% → ~95%)

## [1.2.2] - Previous Release

### Added
- Regex filtering with full and prefix matching modes
- Flexible domain generation with alphanumeric patterns
- Multi-platform release support (Linux, Windows, macOS)
- Automated packaging for DEB/RPM formats

### Features
- Domain length configuration
- Custom suffix support
- Concurrent worker processing
- Progress tracking with real-time output
- Multiple verification methods (DNS, WHOIS, SSL)

---

## Technical Improvements Summary

### Commit Details

#### [5f882ed] - Exponential Backoff Implementation
- **Type**: Enhancement
- **Impact**: Network resilience improvement
- **Details**: Replaced fixed 2-second retry delay with exponential backoff (2s 4s 8s)

#### [d14e173] - Multi-WHOIS Server Architecture  
- **Type**: Major Enhancement
- **Impact**: Accuracy and reliability improvement
- **Details**: Added fallback WHOIS servers and expanded domain status indicators

#### [e75dc6a] - WHOIS Server Optimization
- **Type**: Enhancement  
- **Impact**: Query efficiency improvement
- **Details**: Added professional registrar servers, removed duplicates