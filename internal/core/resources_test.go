package core

import (
	"math"
	"testing"
)

func TestCapacityIncludesBothContainersAndHostReserve(t *testing.T) {
	h := HostCapacity{CPUs: 4, MemoryBytes: 4 << 30, ReservedCPUs: .5, ReservedMemoryBytes: 1 << 30, AllocatedCPUs: 1, AllocatedMemoryBytes: 1 << 30, DockerDiskFreeBytes: 4 << 30, DataDiskFreeBytes: 4 << 30}
	if err := h.Check(Resources{MemoryMB: 768, CPUs: 1}); err != nil {
		t.Fatal(err)
	}
	if err := h.Check(Resources{MemoryMB: 1536, CPUs: 1}); err == nil {
		t.Fatal("accepted RAM oversubscription")
	}
	if err := h.Check(Resources{MemoryMB: 384, CPUs: 1.5}); err == nil {
		t.Fatal("accepted CPU oversubscription")
	}
	h.DataDiskFreeBytes = 1 << 30
	if err := h.Check(Resources{MemoryMB: 384, CPUs: .5}); err == nil {
		t.Fatal("accepted low disk space")
	}
}

func TestResourcesRejectInvalidAndNonFiniteInputs(t *testing.T) {
	for _, r := range []Resources{{255, 1}, {8193, 1}, {512, math.NaN()}, {512, math.Inf(1)}, {512, .3}, {512, 0}} {
		if r.Validate() == nil {
			t.Fatalf("accepted %#v", r)
		}
	}
	for _, r := range []Resources{{256, .25}, {8192, 8}, {512, 1.25}} {
		if err := r.Validate(); err != nil {
			t.Fatal(err)
		}
	}
}
