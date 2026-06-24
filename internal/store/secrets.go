package store

import (
	"log"
	"strings"

	"github.com/AppsGanin/rospanel/internal/datasec"
)

func encField(s string) string {
	out, err := datasec.Encrypt(s)
	if err != nil {
		return s
	}
	return out
}

func decField(s string) string {
	if s == "" || !strings.HasPrefix(s, "enc:v1:") {
		return s
	}
	out, err := datasec.Decrypt(s)
	if err != nil {
		log.Printf("[ERROR] decrypt secret field failed (wrong or missing secrets.key?): %v", err)
		return ""
	}
	return out
}
