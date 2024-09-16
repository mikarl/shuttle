package shuttlefolder

import (
	"encoding/hex"
	"fmt"
	"path"
	"runtime"
)

const (
	TaskBinaryDir    = "binaries"
	TaskBinaryPrefix = "actions"
)

func CalculateBinaryPath(shuttledir, hash string) string {
	binaryName := fmt.Sprintf("%s-%s", TaskBinaryPrefix, hex.EncodeToString([]byte(hash)[:16]))
	if runtime.GOOS == "windows" {
		binaryName = fmt.Sprintf("%s.exe", binaryName)
	}

	return path.Join(
		shuttledir,
		"binaries",
		binaryName,
	)
}
