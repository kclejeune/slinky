//go:build !linux

package cipher

import "errors"

func NewAgeKeyctl() (*AgeKeyctl, error) {
	return nil, errors.New("keyctl cipher is only available on Linux")
}
