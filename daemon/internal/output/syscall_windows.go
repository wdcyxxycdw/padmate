package output

//go:generate go run golang.org/x/sys/windows/mkwinsyscall -output zsyscall_windows.go syscall_windows.go

//sys	sendInput(n uint32, inputs *input, size int32) (count uint32, err error) = user32.SendInput
//sys	getAsyncKeyState(vk int32) (state int16) = user32.GetAsyncKeyState
//sys	mapVirtualKeyEx(code uint32, mapType uint32, hkl windows.Handle) (ret uint32) = user32.MapVirtualKeyExW
//sys	lsaGetLogonSessionData(luid *windows.LUID, data **securityLogonSessionData) (ntstatus error) = secur32.LsaGetLogonSessionData
//sys	lsaFreeReturnBuffer(buf *securityLogonSessionData) (ntstatus error) = secur32.LsaFreeReturnBuffer
