package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/wdcyxxycdw/padmate/daemon/internal/amgp"
)

// readAmgpFile reads an AntiMicroX file for profile import-amgp, at most
// amgp.MaxBytes; the daemon checks its contents.
func readAmgpFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, amgp.MaxBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > amgp.MaxBytes {
		return nil, fmt.Errorf("file must be at most %d MiB", amgp.MaxBytes>>20)
	}
	return data, nil
}

// printAmgpImport prints the result of profiles.importAmgp, result, as text:
// what was made, then the report as amgp.FormatReport writes it.
func printAmgpImport(result []byte, stdout io.Writer) error {
	var imported struct {
		ID     string            `json:"id"`
		Name   string            `json:"name"`
		Report []amgp.ReportLine `json:"report"`
	}
	if err := json.Unmarshal(result, &imported); err != nil {
		return fmt.Errorf("unexpected import result: %w", err)
	}
	dropped, approximate, info := amgp.Counts(imported.Report)
	fmt.Fprintf(stdout, "导入为配置“%s”（ID：%s）。丢弃 %d 条，近似 %d 条，信息 %d 条。\n", imported.Name, imported.ID, dropped, approximate, info)
	if len(imported.Report) > 0 {
		fmt.Fprintf(stdout, "\n%s", amgp.FormatReport(imported.Report))
	}
	return nil
}
