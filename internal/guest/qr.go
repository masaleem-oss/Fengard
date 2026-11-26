package guest

import (
	"crypto/rand"
	"errors"
	"strings"
)

// QR is the text phones read from a wi-fi qr code. backslash, semicolon, comma, colon and
// double quote are escaped so any name or password survives
func QR(ssid, password string) string {
	esc := strings.NewReplacer(`\`, `\\`, `;`, `\;`, `,`, `\,`, `:`, `\:`, `"`, `\"`)
	return "WIFI:T:WPA;S:" + esc.Replace(ssid) + ";P:" + esc.Replace(password) + ";;"
}

// no 0 o 1 l or i so it can be read off a screen and typed
const pwChars = "abcdefghjkmnpqrstuvwxyz23456789"

// NewPassword is 16 random characters in groups of four, about 79 bits
func NewPassword() string {
	var out strings.Builder
	b := make([]byte, 1)
	for n := 0; n < 16; {
		rand.Read(b)
		// 248 is 31 times 8, anything above would make some letters likelier
		if b[0] >= 248 {
			continue
		}
		if n > 0 && n%4 == 0 {
			out.WriteByte('-')
		}
		out.WriteByte(pwChars[int(b[0])%len(pwChars)])
		n++
	}
	return out.String()
}

// CheckPassword is wpa2's rule for a passphrase: 8 to 63 printable ascii characters
func CheckPassword(p string) error {
	if len(p) < 8 || len(p) > 63 {
		return errors.New("the password has to be 8 to 63 characters")
	}
	for _, c := range p {
		if c < 0x20 || c > 0x7e {
			return errors.New("the password can only use letters, numbers, spaces and the usual symbols")
		}
	}
	return nil
}
