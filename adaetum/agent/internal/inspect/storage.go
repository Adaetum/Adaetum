package inspect

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
)

// Volume is a PersistentVolumeClaim joined with what Longhorn knows about the
// backing volume. Sizes are bytes.
type Volume struct {
	Namespace    string `json:"namespace"`
	Name         string `json:"name"`
	Phase        string `json:"phase"`
	StorageClass string `json:"storageClass,omitempty"`
	// Capacity is the size as Kubernetes prints it (for example "10Gi");
	// CapacityBytes is the same value in bytes, or zero if it could not be read.
	Capacity      string `json:"capacity,omitempty"`
	CapacityBytes uint64 `json:"capacityBytes,omitempty"`
	// UsedBytes is how much data the volume actually holds. It is nil, not
	// zero, when unknown (not Longhorn-backed, or Longhorn unreachable), so an
	// empty volume is distinguishable from an unmeasured one.
	UsedBytes *uint64 `json:"usedBytes,omitempty"`
	// Replicas is how many copies Longhorn keeps. The disk space a volume
	// really occupies is UsedBytes times Replicas.
	Replicas int `json:"replicas,omitempty"`
	// Robustness is Longhorn's healthy, degraded, or faulted. Empty when the
	// volume is not Longhorn-backed or Longhorn could not be queried.
	Robustness string `json:"robustness,omitempty"`
	// Expandable is whether the storage class lets the volume be made bigger in place.
	// Shrinking is never possible in place. Nil when the class could not be read.
	Expandable *bool `json:"expandable,omitempty"`
	// PVName is the bound PersistentVolume, which is also the volume's name in Longhorn
	// (the page uses it to link to Longhorn's own view of it).
	PVName string `json:"pvName,omitempty"`
}

// StorageDisk is one disk Longhorn stores volumes on. The fields keep
// Longhorn's own meanings so the UI can explain them in plain language:
//
//	MaxBytes       the disk's total size
//	ReservedBytes  held back for the operating system; Longhorn will not place
//	               volumes in it
//	ScheduledBytes the sum of volume sizes promised to apps (thin-provisioned,
//	               so usually far more than is actually written)
//	AvailableBytes free space on the filesystem right now
type StorageDisk struct {
	Node           string `json:"node"`
	Path           string `json:"path"`
	MaxBytes       uint64 `json:"maxBytes"`
	ReservedBytes  uint64 `json:"reservedBytes"`
	ScheduledBytes uint64 `json:"scheduledBytes"`
	AvailableBytes uint64 `json:"availableBytes"`
	// Schedulable is false when Longhorn will not place new volumes here.
	Schedulable bool `json:"schedulable"`
	// ImageBytes is the space the node's container image store occupies, as the
	// kubelet reports it. It is set only when the image store is on this same
	// filesystem, so it can be subtracted from this disk's used space; it is
	// zero when unknown or elsewhere.
	ImageBytes uint64 `json:"imageBytes,omitempty"`
}

// StorageCapacity is the cluster's storage planning picture.
type StorageCapacity struct {
	Disks []StorageDisk `json:"disks"`
	// OverProvisionPercent is how far Longhorn lets promised space exceed the
	// usable space: 100 means promises may not exceed it, 200 means twice it.
	OverProvisionPercent int `json:"overProvisionPercent"`
}

type longhornVolume struct {
	robustness  string
	actualBytes uint64
	replicas    int
}

// collectVolumes lists PVCs and, when Longhorn answers, adds its health and
// measured usage. A Longhorn failure is recorded but does not hide the list.
func (i *Inspector) collectVolumes(ctx context.Context, fail func(string, error)) []Volume {
	data, err := i.kubectlGet(ctx, "persistentvolumeclaims", "-A")
	if err != nil {
		fail("volumes", err)
		return nil
	}
	volumes, backing, err := parseVolumes(data)
	if err != nil {
		fail("volumes", err)
		return nil
	}

	i.attachExpandability(ctx, volumes, fail)

	data, err = i.kubectlGet(ctx, "volumes.longhorn.io", "-n", "longhorn-system")
	if err != nil {
		fail("longhorn volumes", err)
		return volumes
	}
	measured, err := parseLonghornVolumes(data)
	if err != nil {
		fail("longhorn volumes", err)
		return volumes
	}
	for index := range volumes {
		if info, ok := measured[backing[index]]; ok {
			volumes[index].Robustness = info.robustness
			used := info.actualBytes
			volumes[index].UsedBytes = &used
			volumes[index].Replicas = info.replicas
		}
	}
	return volumes
}

// attachExpandability marks each volume with whether its storage class allows growing
// it in place, which decides whether "make it bigger" is a real option.
func (i *Inspector) attachExpandability(ctx context.Context, volumes []Volume, fail func(string, error)) {
	data, err := i.kubectlGet(ctx, "storageclasses")
	if err != nil {
		fail("storage classes", err)
		return
	}
	expandable, err := parseStorageClasses(data)
	if err != nil {
		fail("storage classes", err)
		return
	}
	for index := range volumes {
		if allowed, ok := expandable[volumes[index].StorageClass]; ok {
			value := allowed
			volumes[index].Expandable = &value
		}
	}
}

// parseStorageClasses reads `kubectl get storageclasses -o json` as name -> whether
// volumes of that class can be expanded.
func parseStorageClasses(data []byte) (map[string]bool, error) {
	var list struct {
		Items []struct {
			Metadata objectMeta `json:"metadata"`
			Allow    *bool      `json:"allowVolumeExpansion"`
		} `json:"items"`
	}
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, err
	}
	classes := make(map[string]bool, len(list.Items))
	for _, item := range list.Items {
		classes[item.Metadata.Name] = item.Allow != nil && *item.Allow
	}
	return classes, nil
}

// collectCapacity reads Longhorn's disk accounting. Without Longhorn it returns
// nil and records why, and the UI falls back to the volume list alone.
func (i *Inspector) collectCapacity(ctx context.Context, fail func(string, error)) *StorageCapacity {
	data, err := i.kubectlGet(ctx, "nodes.longhorn.io", "-n", "longhorn-system")
	if err != nil {
		fail("storage capacity", err)
		return nil
	}
	disks, err := parseLonghornDisks(data)
	if err != nil {
		fail("storage capacity", err)
		return nil
	}

	i.attachImageUsage(ctx, disks, fail)

	// 100 is the safe reading when the setting cannot be read: it assumes no
	// over-provisioning, so the UI never claims more headroom than exists.
	capacity := &StorageCapacity{Disks: disks, OverProvisionPercent: 100}
	data, err = i.kubectlGet(ctx, "settings.longhorn.io", "storage-over-provisioning-percentage", "-n", "longhorn-system")
	if err != nil {
		fail("storage over-provisioning setting", err)
		return capacity
	}
	if percent, err := parseSettingPercent(data); err != nil {
		fail("storage over-provisioning setting", err)
	} else {
		capacity.OverProvisionPercent = percent
	}
	return capacity
}

// parseVolumes returns the claims plus, index-aligned, the name of the bound
// PersistentVolume (the key Longhorn uses for its own volume object).
func parseVolumes(data []byte) ([]Volume, []string, error) {
	var list struct {
		Items []struct {
			Metadata objectMeta `json:"metadata"`
			Spec     struct {
				StorageClassName *string `json:"storageClassName"`
				VolumeName       string  `json:"volumeName"`
			} `json:"spec"`
			Status struct {
				Phase    string            `json:"phase"`
				Capacity map[string]string `json:"capacity"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, nil, err
	}

	volumes := make([]Volume, 0, len(list.Items))
	backing := make([]string, 0, len(list.Items))
	for _, item := range list.Items {
		volume := Volume{
			Namespace: item.Metadata.Namespace,
			Name:      item.Metadata.Name,
			Phase:     item.Status.Phase,
			Capacity:  item.Status.Capacity["storage"],
		}
		volume.CapacityBytes, _ = parseQuantity(volume.Capacity)
		if item.Spec.StorageClassName != nil {
			volume.StorageClass = *item.Spec.StorageClassName
		}
		volume.PVName = item.Spec.VolumeName
		volumes = append(volumes, volume)
		backing = append(backing, item.Spec.VolumeName)
	}
	return volumes, backing, nil
}

// parseLonghornVolumes maps Longhorn volume name to its health and the bytes it
// actually holds (including snapshots).
func parseLonghornVolumes(data []byte) (map[string]longhornVolume, error) {
	var list struct {
		Items []struct {
			Metadata objectMeta `json:"metadata"`
			Spec     struct {
				NumberOfReplicas int `json:"numberOfReplicas"`
			} `json:"spec"`
			Status struct {
				Robustness string `json:"robustness"`
				ActualSize uint64 `json:"actualSize"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, err
	}
	volumes := make(map[string]longhornVolume, len(list.Items))
	for _, item := range list.Items {
		volumes[item.Metadata.Name] = longhornVolume{
			robustness:  item.Status.Robustness,
			actualBytes: item.Status.ActualSize,
			replicas:    item.Spec.NumberOfReplicas,
		}
	}
	return volumes, nil
}

// parseLonghornDisks reads `kubectl get nodes.longhorn.io -o json`. Reserved
// space and scheduling permission live in each disk's spec; size, free space
// and promised space live in its status. A disk without status yet (just
// added) is skipped because its numbers would be meaningless.
func parseLonghornDisks(data []byte) ([]StorageDisk, error) {
	var list struct {
		Items []struct {
			Metadata objectMeta `json:"metadata"`
			Spec     struct {
				AllowScheduling bool `json:"allowScheduling"`
				Disks           map[string]struct {
					Path            string `json:"path"`
					AllowScheduling bool   `json:"allowScheduling"`
					StorageReserved uint64 `json:"storageReserved"`
				} `json:"disks"`
			} `json:"spec"`
			Status struct {
				DiskStatus map[string]struct {
					StorageMaximum   uint64 `json:"storageMaximum"`
					StorageAvailable uint64 `json:"storageAvailable"`
					StorageScheduled uint64 `json:"storageScheduled"`
					Conditions       []struct {
						Type   string `json:"type"`
						Status string `json:"status"`
					} `json:"conditions"`
				} `json:"diskStatus"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, err
	}

	disks := []StorageDisk{}
	for _, node := range list.Items {
		// Map iteration order is random; sort so reports are deterministic.
		names := make([]string, 0, len(node.Status.DiskStatus))
		for name := range node.Status.DiskStatus {
			names = append(names, name)
		}
		sort.Strings(names)

		for _, name := range names {
			status := node.Status.DiskStatus[name]
			spec := node.Spec.Disks[name]
			schedulable := node.Spec.AllowScheduling && spec.AllowScheduling
			for _, condition := range status.Conditions {
				if condition.Type == "Schedulable" && condition.Status != "True" {
					schedulable = false
				}
			}
			disks = append(disks, StorageDisk{
				Node:           node.Metadata.Name,
				Path:           spec.Path,
				MaxBytes:       status.StorageMaximum,
				ReservedBytes:  spec.StorageReserved,
				ScheduledBytes: status.StorageScheduled,
				AvailableBytes: status.StorageAvailable,
				Schedulable:    schedulable,
			})
		}
	}
	return disks, nil
}

// parseSettingPercent reads a single Longhorn setting object whose value is a
// number, such as storage-over-provisioning-percentage.
func parseSettingPercent(data []byte) (int, error) {
	var setting struct {
		Value string `json:"value"`
	}
	if err := json.Unmarshal(data, &setting); err != nil {
		return 0, err
	}
	percent, err := strconv.Atoi(setting.Value)
	if err != nil {
		return 0, fmt.Errorf("setting value %q is not a number", setting.Value)
	}
	return percent, nil
}

var quantityPattern = regexp.MustCompile(`^([0-9]+(?:\.[0-9]+)?)([A-Za-z]*)$`)

var quantityUnits = map[string]float64{
	"":  1,
	"k": 1e3, "M": 1e6, "G": 1e9, "T": 1e12, "P": 1e15, "E": 1e18,
	"Ki": 1 << 10, "Mi": 1 << 20, "Gi": 1 << 30, "Ti": 1 << 40, "Pi": 1 << 50, "Ei": 1 << 60,
}

// parseQuantity converts a Kubernetes quantity such as "10Gi" or "500M" to
// bytes. It returns false for anything else (such as a milli-unit), so callers
// treat an unreadable size as unknown instead of guessing.
func parseQuantity(quantity string) (uint64, bool) {
	match := quantityPattern.FindStringSubmatch(quantity)
	if match == nil {
		return 0, false
	}
	multiplier, known := quantityUnits[match[2]]
	if !known {
		return 0, false
	}
	value, err := strconv.ParseFloat(match[1], 64)
	if err != nil {
		return 0, false
	}
	return uint64(value * multiplier), true
}

// kubeletStats is the part of the kubelet's stats summary this package reads.
type kubeletStats struct {
	Node struct {
		Fs struct {
			CapacityBytes uint64 `json:"capacityBytes"`
		} `json:"fs"`
		Runtime struct {
			ImageFs struct {
				CapacityBytes uint64 `json:"capacityBytes"`
				UsedBytes     uint64 `json:"usedBytes"`
			} `json:"imageFs"`
		} `json:"runtime"`
	} `json:"node"`
}

// nodeNamePattern limits what may be placed in an API path. Node names come
// from the cluster, but they are interpolated into a URL, so only DNS-label
// characters are allowed rather than trusting them.
var nodeNamePattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]*[a-z0-9])?$`)

// attachImageUsage asks each node's kubelet how big its container image store
// is and records it on the node's disks. The kubelet is reached through the API
// server, so it works for every node from one place. A failure is recorded and
// leaves ImageBytes at zero: the breakdown then simply lacks that part.
func (i *Inspector) attachImageUsage(ctx context.Context, disks []StorageDisk, fail func(string, error)) {
	statsByNode := map[string]kubeletStats{}
	for _, disk := range disks {
		if _, done := statsByNode[disk.Node]; done {
			continue
		}
		statsByNode[disk.Node] = kubeletStats{} // mark attempted, even on failure
		if !nodeNamePattern.MatchString(disk.Node) {
			fail("image storage", fmt.Errorf("node name %q is not a valid host name", disk.Node))
			continue
		}
		data, err := i.kubectlRaw(ctx, "/api/v1/nodes/"+disk.Node+"/proxy/stats/summary")
		if err != nil {
			fail("image storage", err)
			continue
		}
		var stats kubeletStats
		if err := json.Unmarshal(data, &stats); err != nil {
			fail("image storage", err)
			continue
		}
		statsByNode[disk.Node] = stats
	}

	for index := range disks {
		stats := statsByNode[disks[index].Node]
		if sameFilesystem(disks[index].MaxBytes, stats.Node.Runtime.ImageFs.CapacityBytes) {
			disks[index].ImageBytes = stats.Node.Runtime.ImageFs.UsedBytes
		}
	}
}

// sameFilesystem reports whether two capacities describe one filesystem. Two
// readings of the same device differ by a few bytes at most, while different
// disks almost never match, so a 1% tolerance is both safe and sufficient. A
// zero capacity means "unknown" and never matches.
func sameFilesystem(a, b uint64) bool {
	if a == 0 || b == 0 {
		return false
	}
	larger, smaller := a, b
	if smaller > larger {
		larger, smaller = smaller, larger
	}
	return larger-smaller <= larger/100
}
