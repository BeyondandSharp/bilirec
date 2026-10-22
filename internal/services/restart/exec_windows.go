//go:build windows

package restart

import "errors"

func Exec() error {
	return errors.New("进程自替换在 Windows 上不受支持")
}
