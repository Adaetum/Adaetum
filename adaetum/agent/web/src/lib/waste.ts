const MI = 1 << 20;
const GIB = 1 << 30;

// How much it matters that something is reserved but unused. Waste at or under the floor costs
// nothing (100 MiB, 100m: too small for anyone to act on), and above it the cost climbs and
// levels off: materiality = 1 - e^(-(waste - floor) / scale), so 0 at the floor, about 63% one
// scale above it, and close to all of it when the waste is several scales over. The idea is that
// a system's requirements are measured in what it can't spare, so 50 MiB used against 200 set
// (150 MiB of waste) is a small mark, and 500 MiB used against 1500 set (1000 MiB) is nearly the
// full one. Disk is judged the same way on a larger scale (it is cheaper to hold and cannot be
// shrunk in place). It only softens the "reserves far more than it uses" side; being at or over a limit
// or request is judged at full weight at any size.
export const MEMORY_WASTE = { floor: 100 * MI, scale: 400 * MI };
export const CPU_WASTE = { floor: 100, scale: 400 }; // millicores
export function materiality(waste: number, { floor, scale }: { floor: number; scale: number }): number {
  return waste <= floor ? 0 : 1 - Math.exp(-(waste - floor) / scale);
}
// Disk: thin Longhorn volumes cost nothing today, so waste matters only once it is a lot.
export const DISK_WASTE = { floor: 2 * GIB, scale: 10 * GIB };
