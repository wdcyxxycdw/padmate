// Command tiny is the program pecheck's tests cross-build for Windows: it binds
// two functions at run time, one in golang.org/x/sys/windows and one of its own,
// as padmate's executables do.
package main

import (
	"fmt"

	"golang.org/x/sys/windows"
)

var procSendInput = windows.NewLazySystemDLL("user32.dll").NewProc("SendInput")

func main() {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, windows.GetCurrentProcessId())
	fmt.Println(h, err, procSendInput.Name)
}
