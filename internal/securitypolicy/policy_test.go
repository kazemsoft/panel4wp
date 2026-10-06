package securitypolicy

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestPolicyPreservesPinnedDefaultExceptExplicitRestrictions(t *testing.T) {
	source, err := os.ReadFile("../../third_party/moby/seccomp-default.json")
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(source)
	if hex.EncodeToString(sum[:]) != "6416b47770785a41ac59073cdc77d9fe98517df2799dc83ef207e622de3053f6" {
		t.Fatal("baseline changed without review")
	}
	var baseline, actual map[string]any
	if json.Unmarshal(source, &baseline) != nil || json.Unmarshal(Bytes(), &actual) != nil {
		t.Fatal("invalid policy")
	}
	rules := actual["syscalls"].([]any)
	var preservedRules []any
	type interval struct{ first, last uint64 }
	var ranges []interval
	for _, r := range rules {
		rule := r.(map[string]any)
		if reflect.DeepEqual(rule["names"], []any{"ioctl"}) {
			args, ok := rule["args"].([]any)
			if rule["action"] != "SCMP_ACT_ALLOW" || !ok || len(args) != 1 || len(rule) != 3 {
				t.Fatal("unsafe ioctl rule")
			}
			arg := args[0].(map[string]any)
			mask := uint64(arg["value"].(float64))
			value := uint64(arg["valueTwo"].(float64))
			inverted := uint64(0xffffffff) ^ mask
			if arg["op"] != "SCMP_CMP_MASKED_EQ" || arg["index"] != float64(1) || mask > 0xffffffff || value&mask != value || inverted&(inverted+1) != 0 {
				t.Fatal("not a low32 prefix partition")
			}
			ranges = append(ranges, interval{value, value + inverted})
		} else {
			preservedRules = append(preservedRules, r)
		}
	}
	sort.Slice(ranges, func(i, j int) bool { return ranges[i].first < ranges[j].first })
	var merged []interval
	for _, r := range ranges {
		if len(merged) > 0 {
			prev := &merged[len(merged)-1]
			if r.first <= prev.last {
				t.Fatal("overlapping ioctl allowances")
			}
			if r.first == prev.last+1 {
				prev.last = r.last
				continue
			}
		}
		merged = append(merged, r)
	}
	// Prove the entire 2^32 space allows exactly every other command, without
	// sampling or duplicating the generator's trie algorithm.
	wantRanges := []interval{{0, 0x40046601}, {0x40046603, 0x40086601}, {0x40086603, 0x401c581f}, {0x401c5821, 0xffffffff}}
	if !reflect.DeepEqual(merged, wantRanges) {
		t.Fatalf("unsafe ioctl command coverage: %v", merged)
	}
	actual["syscalls"] = preservedRules
	var preserved []any
	for _, r := range baseline["syscalls"].([]any) {
		rule := r.(map[string]any)
		var names []any
		for _, n := range rule["names"].([]any) {
			if n != "quotactl" && n != "quotactl_fd" && n != "file_setattr" && n != "ioctl" {
				names = append(names, n)
			}
		}
		if len(names) > 0 {
			rule["names"] = names
			preserved = append(preserved, rule)
		}
	}
	baseline["syscalls"] = preserved
	if !reflect.DeepEqual(baseline, actual) {
		t.Fatal("unrelated Docker protection changed")
	}
}

func TestActiveRequiresActualProfileAndNoNewPrivileges(t *testing.T) {
	var compact bytes.Buffer
	if err := json.Compact(&compact, Bytes()); err != nil {
		t.Fatal(err)
	}
	for _, opts := range [][]string{
		{"seccomp=" + string(Bytes()), "no-new-privileges:true"},
		{"seccomp:" + compact.String(), "no-new-privileges=true"},
	} {
		if !Active(opts) {
			t.Fatal("valid Docker profile rejected")
		}
	}
	for _, opts := range [][]string{
		nil, {"no-new-privileges:true"}, {"seccomp=unconfined", "no-new-privileges:true"},
		{"seccomp=" + Filename, "no-new-privileges:true"},
		{"seccomp=" + compact.String()},
		{"seccomp=" + compact.String(), "no-new-privileges:false"},
		{"seccomp=" + compact.String(), "seccomp=unconfined", "no-new-privileges:true"},
		{"seccomp=" + strings.Replace(compact.String(), `"defaultAction":"SCMP_ACT_ERRNO"`, `"defaultAction":"SCMP_ACT_ALLOW"`, 1), "no-new-privileges:true"},
	} {
		if Active(opts) {
			t.Fatal("missing or tampered filter accepted")
		}
	}
	copy := Bytes()
	copy[0] = 'x'
	if Bytes()[0] == 'x' {
		t.Fatal("embedded profile can be mutated")
	}
}
