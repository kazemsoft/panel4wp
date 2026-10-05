package core

import (
	"errors"
	"math"
	"strings"
)

// Both WordPress and MariaDB receive these limits, separately.
type Resources struct {
	MemoryMB int     `json:"memory_mb"`
	CPUs     float64 `json:"cpus"`
}

func (r Resources) Validate() error {
	if r.MemoryMB < 256 || r.MemoryMB > 8192 {
		return errors.New("memory must be between 256 and 8192 MiB per container")
	}
	if math.IsNaN(r.CPUs) || math.IsInf(r.CPUs, 0) || r.CPUs < .25 || r.CPUs > 8 || math.Abs(r.CPUs*4-math.Round(r.CPUs*4)) > .000001 {
		return errors.New("CPU must be between 0.25 and 8, in steps of 0.25, per container")
	}
	return nil
}

func SiteResources(s Site) Resources { m, c := ResourceLimits(s); return Resources{m, c} }

type ResourceRequest struct {
	Site      Site      `json:"site"`
	Resources Resources `json:"resources"`
}

type ResourcePlan struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Resources Resources `json:"resources"`
}

func (p ResourcePlan) Validate() error {
	if !ValidID(p.ID) || strings.TrimSpace(p.Name) == "" || len([]rune(p.Name)) > 60 || strings.ContainsAny(p.Name, "\n\r\x00") {
		return errors.New("resource plan needs a valid ID and a name of 1 to 60 characters")
	}
	return p.Resources.Validate()
}

type HostCapacity struct {
	OtherMemoryBytes     int64   `json:"other_memory_bytes"`
	OtherCPUs            float64 `json:"other_cpus"`
	CPUs                 float64 `json:"cpus"`
	MemoryBytes          int64   `json:"memory_bytes"`
	ReservedCPUs         float64 `json:"reserved_cpus"`
	ReservedMemoryBytes  int64   `json:"reserved_memory_bytes"`
	AllocatedCPUs        float64 `json:"allocated_cpus"`
	AllocatedMemoryBytes int64   `json:"allocated_memory_bytes"`
	DockerDiskFreeBytes  int64   `json:"docker_disk_free_bytes"`
	DataDiskFreeBytes    int64   `json:"data_disk_free_bytes"`
}

func (h HostCapacity) AvailableMemoryBytes() int64 {
	return max(0, h.MemoryBytes-h.ReservedMemoryBytes-h.AllocatedMemoryBytes-h.OtherMemoryBytes)
}
func (h HostCapacity) AvailableCPUs() float64 {
	return max(0, h.CPUs-h.ReservedCPUs-h.AllocatedCPUs-h.OtherCPUs)
}

func (h HostCapacity) Check(r Resources) error {
	if err := r.Validate(); err != nil {
		return err
	}
	if h.MemoryBytes <= 0 || h.CPUs <= 0 {
		return errors.New("host capacity is unavailable")
	}
	if int64(r.MemoryMB)*2*1024*1024 > h.AvailableMemoryBytes() {
		return errors.New("insufficient RAM capacity for both site containers and the host reserve")
	}
	if r.CPUs*2 > h.AvailableCPUs()+.000001 {
		return errors.New("insufficient CPU capacity for both site containers and the host reserve")
	}
	if min(h.DockerDiskFreeBytes, h.DataDiskFreeBytes) < 2*1024*1024*1024 {
		return errors.New("at least 2 GiB free is required on Docker and panel data storage")
	}
	return nil
}
