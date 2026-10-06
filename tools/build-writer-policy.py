#!/usr/bin/env python3
"""Derive the writer policy from the pinned, licensed Moby baseline."""
import hashlib
import json
from pathlib import Path

repo = Path(__file__).resolve().parent.parent
source = repo / "third_party/moby/seccomp-default.json"
baseline = source.read_bytes()
if hashlib.sha256(baseline).hexdigest() != "6416b47770785a41ac59073cdc77d9fe98517df2799dc83ef207e622de3053f6":
    raise SystemExit("Pinned Moby baseline changed; review and update provenance before generation")
profile = json.loads(baseline)
assert profile["defaultAction"] == "SCMP_ACT_ERRNO" and profile["defaultErrnoRet"] == 1
# Restrict quota control even when a caller has extra capabilities.
blocked_syscalls = {"quotactl", "quotactl_fd", "file_setattr", "ioctl"}
ioctl_rules = [rule for rule in profile["syscalls"] if "ioctl" in rule["names"]]
assert len(ioctl_rules) == 1 and ioctl_rules[0]["action"] == "SCMP_ACT_ALLOW" and "args" not in ioctl_rules[0]
for rule in profile["syscalls"]:
    rule["names"] = [name for name in rule["names"] if name not in blocked_syscalls]
profile["syscalls"] = [rule for rule in profile["syscalls"] if rule["names"]]
# A generic ioctl allow can subsume argument-specific deny rules in libseccomp.
# Allow the complement instead. A prefix trie partitions the low 32-bit command
# space into disjoint masked-equality regions, excluding precisely these values.
# Upper bits must be ignored because the kernel truncates ioctl commands.
blocked_commands = {0x401C5820, 0x40086602, 0x40046602}

def allow_prefix(prefix, bits, commands):
    if not commands:
        mask = (0xFFFFFFFF << (32 - bits)) & 0xFFFFFFFF
        profile["syscalls"].append({
            "names": ["ioctl"], "action": "SCMP_ACT_ALLOW",
            "args": [{"index": 1, "value": mask, "valueTwo": prefix, "op": "SCMP_CMP_MASKED_EQ"}],
        })
    elif bits < 32:
        bit = 1 << (31 - bits)
        for value in [prefix, prefix | bit]:
            matching = {command for command in commands if (command & bit) == (value & bit)}
            allow_prefix(value, bits + 1, matching)

allow_prefix(0, 0, blocked_commands)
output = repo / "internal/securitypolicy/writer-v1.json"
output.write_text(json.dumps(profile, indent=2) + "\n")
print("writer policy generated from baseline SHA256 " + hashlib.sha256(baseline).hexdigest())
