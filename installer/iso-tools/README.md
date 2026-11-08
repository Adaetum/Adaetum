# Bundled ISO tools

macOS, Linux and x86-64 Windows use bundled ISO executables. Windows uses
the Git for Windows runtime that setup installs; it does not need Docker,
Podman, MSYS2, or an ISO tool installation.

`xorriso` is GNU xorriso 1.5.8.pl02, GPL-3.0-or-later; its complete source and
license are in `source/xorriso-1.5.8.pl02.tar.gz` (SHA-256
`b1455ecafbf0692ddafe1d71002a96f2ce2d77f4deae602678261ce033f97bc8`). It is
built with its bundled libraries and without optional readline, ACL, xattr or
zlib support. The executable links only macOS system libraries.

`iso-checksum` wraps the upstream `isomd5sum` 1.2.5 libraries, GPL-2.0-or-later,
to implant and verify Rocky's media checksum. Its source and license are in
`source/isomd5sum/`. The portability changes replace the Linux-only endian
header and use a normally allocated sector buffer: macOS Apple Silicon uses a
16 KiB page, larger than the ISO's 2 KiB sectors.

Run `./installer/iso-tools/rebuild-darwin.sh` on macOS with
Xcode command line tools to rebuild both executables from the included sources.

Run `./installer/iso-tools/rebuild-linux.sh` on the target Linux architecture
with a musl C toolchain and Linux headers to rebuild its static executables.
The included Linux executables link musl 1.2.5; its source and license are in
`source/musl-1.2.5.tar.gz` and `source/MUSL-COPYRIGHT`.

The Windows xorriso and bzip2 DLL come from the pinned MSYS2 xorriso
1.5.8.2-1 and libbz2 1.0.8-4 packages. Their corresponding source packages
are included under `source/`; their other runtime libraries are supplied by
Git for Windows. Run `rebuild-windows.sh` on Linux with curl, zstd, and a
MinGW cross-compiler to retrieve the checked binaries and rebuild the Windows
checksum utility. The checksum utility uses 64-bit file offsets on Windows.
