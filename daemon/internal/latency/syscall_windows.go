package latency

//go:generate go run golang.org/x/sys/windows/mkwinsyscall -output zsyscall_windows.go syscall_windows.go

//sys	queryPerformanceCounter(count *int64) (err error) = kernel32.QueryPerformanceCounter
//sys	queryPerformanceFrequency(freq *int64) (err error) = kernel32.QueryPerformanceFrequency
