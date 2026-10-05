package core

// StorageCapability describes the backing filesystem, not an active site quota.
// Support alone does not make a quota safe to activate.
type StorageCapability struct {
	WordPressFilesystem string `json:"wordpress_filesystem"`
	DatabaseFilesystem  string `json:"database_filesystem"`
	SameFilesystem      bool   `json:"same_filesystem"`
	ProjectAccounting   bool   `json:"project_accounting"`
	ProjectEnforcement  bool   `json:"project_enforcement"`
	Reason              string `json:"reason"`
}

func (c StorageCapability) SupportsProjectQuotas() bool {
	return c.Reason == "supported" && c.WordPressFilesystem == "xfs" && c.DatabaseFilesystem == "xfs" && c.SameFilesystem && c.ProjectAccounting && c.ProjectEnforcement
}
