package auth

import "time"

// only for tests in other packages
func CodeForTest(secret string, offset int64) string {
	raw, _ := b32.DecodeString(secret)
	return totpCode(raw, time.Now().Unix()/totpStep+offset)
}
