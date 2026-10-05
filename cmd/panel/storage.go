package main

func diskCapabilityReasonKey(reason string) string {
	switch reason {
	case "filesystem", "kernel", "enforcement-disabled":
		return "disk_quota_unsupported"
	case "different-filesystems":
		return "disk_quota_separate_filesystems"
	case "volume-driver":
		return "disk_quota_driver"
	default:
		return "disk_quota_check_failed"
	}
}
