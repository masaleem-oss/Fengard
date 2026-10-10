//go:build !linux

package update

import "errors"

func detach(string) error { return errors.New("updates install on routers only") }
