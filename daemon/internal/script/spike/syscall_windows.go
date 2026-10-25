package spike

//go:generate go run golang.org/x/sys/windows/mkwinsyscall -output zsyscall_windows.go syscall_windows.go

//sys	queryThreadCycleTime(thread windows.Handle, cycles *uint64) (err error) = kernel32.QueryThreadCycleTime
//sys	queryPerformanceCounter(count *int64) (err error) = kernel32.QueryPerformanceCounter
//sys	queryPerformanceFrequency(freq *int64) (err error) = kernel32.QueryPerformanceFrequency
//sys	getThreadTimes(thread windows.Handle, creation *windows.Filetime, exit *windows.Filetime, kernel *windows.Filetime, user *windows.Filetime) (err error) = kernel32.GetThreadTimes
//sys	setThreadAffinityMask(thread windows.Handle, mask uintptr) (prev uintptr, err error) = kernel32.SetThreadAffinityMask
//sys	getCurrentProcessorNumber() (n uint32) = kernel32.GetCurrentProcessorNumber
//sys	getSystemCpuSetInformation(info *byte, length uint32, returned *uint32, process windows.Handle, flags uint32) (err error) = kernel32.GetSystemCpuSetInformation
//sys	ntQueryTimerResolution(minimum *uint32, maximum *uint32, current *uint32) (ntstatus error) = ntdll.NtQueryTimerResolution
