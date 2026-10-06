// Package securitypolicy owns the versioned seccomp policy for site writers.
package securitypolicy

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"strings"
)

//go:generate python3 ../../tools/build-writer-policy.py
//go:embed writer-v1.json
var writerProfile []byte

const Filename = "writer-seccomp-v1.json"

func Bytes() []byte { return bytes.Clone(writerProfile) }

// Active compares Docker's actual seccomp content, not a label asserting that
// a profile was configured. Docker accepts either ':' or '=' option separators.
func Active(options []string) bool {
	var expected any
	if json.Unmarshal(writerProfile, &expected) != nil {
		return false
	}
	want, _ := json.Marshal(expected)
	found, noNewPrivileges := false, false
	for _, option := range options {
		if option == "no-new-privileges=true" || option == "no-new-privileges:true" || option == "no-new-privileges" {
			noNewPrivileges = true
		}
		if strings.HasPrefix(option, "seccomp=") || strings.HasPrefix(option, "seccomp:") {
			if found {
				return false
			}
			found = true
			var actual any
			if json.Unmarshal([]byte(option[len("seccomp="):]), &actual) != nil {
				return false
			}
			got, _ := json.Marshal(actual)
			if !bytes.Equal(want, got) {
				return false
			}
		}
	}
	return found && noNewPrivileges
}
