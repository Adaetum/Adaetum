import { ContainerImages, StorageCapacity, Volume } from "../api";

// Plain-language storage model. Two separate questions, deliberately not mixed:
//
//   planning: how is space set aside?  Total, Reserved, Allocated, Unallocated
//   reality:  how much is really used? Apps, System & other, Free
//
// Everything here is about the disks Longhorn manages. Volumes on other storage
// (for example a shared file store) are not on those disks, so they never count
// toward these totals.

export interface DiskSummary {
  node: string;
  path: string;
  schedulable: boolean;
  totalBytes: number;
  reservedBytes: number;
  allocatedBytes: number;
  usedBytes: number;
  // Unpacked container images on this disk, from the kubelet; zero when unknown.
  imageBytes: number;
  // Compressed copies containerd keeps beside them. Measured only on the server
  // the agent runs on, so zero elsewhere. Together with imageBytes this is the
  // real space container images take.
  imageCopiesBytes: number;
  imagesTotalBytes: number;
  // Estimated share of those images no container uses. Only set for the server
  // the agent reports on, because only that server's runtime can say.
  unusedImageBytes?: number;
  unusedImageShare?: number;
  usedFraction: number;
}

export interface StorageSummary {
  // Planning view.
  totalBytes: number;
  reservedBytes: number;
  allocatedBytes: number;
  // Space neither reserved nor promised to a volume. Never negative: promises
  // may exceed usable space when Longhorn over-provisioning is on.
  unallocatedBytes: number;
  overProvisionPercent: number;
  // True when promises exceed the usable space (allowed by over-provisioning).
  overAllocated: boolean;
  // Reality view.
  usedBytes: number;
  freeBytes: number;
  appsBytes: number;
  // True when some servers' compressed image copies were not measured, so part
  // of what is shown as "system and other" there is really image data.
  imageCopiesPartial: boolean;
  // Downloaded container images. They are rebuildable from their registries, so
  // they are called out separately from the rest of the system's files.
  imagesBytes: number;
  systemBytes: number;
  usedFraction: number;
  // One full disk is what causes trouble, and an average can hide it, so status
  // everywhere is judged by the fullest disk, not by usedFraction.
  fullest: DiskSummary | undefined;
  fullestFraction: number;
  disks: DiskSummary[];
}

export interface StorageOptions {
  // Container image usage for the machine the agent runs on.
  images?: ContainerImages;
  hostname?: string;
}

// Kubernetes and the OS may name a machine differently (short name versus a
// fully qualified one), so a node matches when either name extends the other
// at a dot boundary.
export function isThisServer(node: string, hostname: string | undefined): boolean {
  if (!hostname) return false;
  return node === hostname || hostname.startsWith(`${node}.`) || node.startsWith(`${hostname}.`);
}

// estimateUnusedImageBytes scales the kubelet's real image-store size by the
// unused share the runtime reports. The runtime's own sizes are compressed, so
// they would understate disk use; the share is the part that carries over.
export function estimateUnusedImage(images: ContainerImages, imageStoreBytes: number): { bytes: number; share: number } | undefined {
  if (images.totalBytes <= 0 || imageStoreBytes <= 0) return undefined;
  const share = Math.min(images.unusedBytes / images.totalBytes, 1);
  return { bytes: Math.round(imageStoreBytes * share), share };
}

export function isLonghornVolume(volume: Volume): boolean {
  return volume.usedBytes !== undefined;
}

// A volume's real footprint on disk is its data times its replica count.
export function volumeFootprintBytes(volume: Volume): number {
  return (volume.usedBytes ?? 0) * Math.max(volume.replicas ?? 1, 1);
}

export function summarizeStorage(capacity: StorageCapacity, volumes: Volume[], options: StorageOptions = {}): StorageSummary {
  const disks: DiskSummary[] = capacity.disks.map((disk) => {
    const usedBytes = Math.max(0, disk.maxBytes - disk.availableBytes);
    const thisServer = Boolean(options.images) && isThisServer(disk.node, options.hostname);
    const imageBytes = disk.imageBytes ?? 0;
    // Only the server the agent runs on can measure its compressed copies.
    const imageCopiesBytes = thisServer ? (options.images?.contentBytes ?? 0) : 0;
    const imagesTotalBytes = imageBytes + imageCopiesBytes;
    // Removing an image frees both its unpacked layers and its compressed copy,
    // so the unused share applies to the total.
    const unused = thisServer && options.images ? estimateUnusedImage(options.images, imagesTotalBytes) : undefined;
    return {
      node: disk.node,
      path: disk.path,
      schedulable: disk.schedulable,
      totalBytes: disk.maxBytes,
      reservedBytes: disk.reservedBytes,
      allocatedBytes: disk.scheduledBytes,
      usedBytes,
      imageBytes,
      imageCopiesBytes,
      imagesTotalBytes,
      unusedImageBytes: unused?.bytes,
      unusedImageShare: unused?.share,
      usedFraction: disk.maxBytes > 0 ? usedBytes / disk.maxBytes : 0,
    };
  });

  const sum = (pick: (disk: DiskSummary) => number) => disks.reduce((total, disk) => total + pick(disk), 0);
  const totalBytes = sum((disk) => disk.totalBytes);
  const reservedBytes = sum((disk) => disk.reservedBytes);
  const allocatedBytes = sum((disk) => disk.allocatedBytes);
  const usedBytes = sum((disk) => disk.usedBytes);
  const usableBytes = Math.max(0, totalBytes - reservedBytes);

  const fullest = [...disks].sort((a, b) => b.usedFraction - a.usedFraction)[0];
  const appsRaw = volumes.filter(isLonghornVolume).reduce((total, volume) => total + volumeFootprintBytes(volume), 0);
  // Each part is clamped to what is left, so stale or overlapping readings can
  // never make the parts add up to more than the space used.
  const appsBytes = Math.min(appsRaw, usedBytes);
  const imagesBytes = Math.min(sum((disk) => disk.imagesTotalBytes), usedBytes - appsBytes);

  return {
    totalBytes,
    reservedBytes,
    allocatedBytes,
    unallocatedBytes: Math.max(0, usableBytes - allocatedBytes),
    overProvisionPercent: capacity.overProvisionPercent,
    overAllocated: allocatedBytes > usableBytes,
    usedBytes,
    freeBytes: Math.max(0, totalBytes - usedBytes),
    appsBytes,
    imagesBytes,
    imageCopiesPartial: disks.some((disk) => disk.imageBytes > 0 && disk.imageCopiesBytes === 0),
    systemBytes: Math.max(0, usedBytes - appsBytes - imagesBytes),
    usedFraction: totalBytes > 0 ? usedBytes / totalBytes : 0,
    fullest,
    fullestFraction: fullest?.usedFraction ?? 0,
    disks,
  };
}

export interface AppStorage {
  app: string;
  volumes: Volume[];
  // Totals cover Longhorn volumes only.
  sizeBytes: number;
  dataBytes: number;
  otherCount: number;
}

// groupByApp treats a namespace as an app, which is how this stack is laid out.
export function groupByApp(volumes: Volume[]): AppStorage[] {
  const apps = new Map<string, AppStorage>();
  for (const volume of volumes) {
    const entry = apps.get(volume.namespace) ?? { app: volume.namespace, volumes: [], sizeBytes: 0, dataBytes: 0, otherCount: 0 };
    entry.volumes.push(volume);
    if (isLonghornVolume(volume)) {
      entry.sizeBytes += volume.capacityBytes ?? 0;
      entry.dataBytes += volume.usedBytes ?? 0;
    } else {
      entry.otherCount += 1;
    }
    apps.set(volume.namespace, entry);
  }
  return [...apps.values()];
}

// diskPerCompressedByte is the measured ratio of real image disk to the size the
// runtime reports, for the server the agent runs on. It lets a cleanup say how
// much disk it frees instead of how many compressed bytes it removes. Returns
// undefined when it cannot be measured.
export function diskPerCompressedByte(summary: StorageSummary, images: ContainerImages | undefined): number | undefined {
  const disk = summary.disks.find((candidate) => candidate.unusedImageBytes !== undefined);
  if (!disk || !images || images.totalBytes <= 0 || disk.imagesTotalBytes <= 0) return undefined;
  return disk.imagesTotalBytes / images.totalBytes;
}
