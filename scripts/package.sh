#!/usr/bin/env bash
# Build release artifacts for Linux and Windows (amd64).
# Produces: tar.gz, zip, deb, rpm, msi (if tools available) + SHA256SUMS.txt.
#
# Each archive contains the binary, config.example.json, README.md.
# Native packages (deb, rpm, msi) also include desktop integration
# (desktop file, icons, Start Menu shortcuts).
set -euo pipefail

cd "$(dirname "$0")/.."
VERSION="${1:-$(git describe --tags --always 2>/dev/null || echo dev)}"
OUT="dist"
rm -rf "$OUT"
mkdir -p "$OUT"

# Generate icons from SVG source
echo "=== Generating icons ==="
go run ./cmd/icon-gen --out assets/

# Build helper: creates a staging directory with the binary + docs, then
# runs the archiver command.
build() {
	local goos="$1" ext="$2" arcname="$3"
	local stage="$OUT/tinvest-snapshot"
	mkdir -p "$stage"
	CGO_ENABLED=0 GOOS="$goos" GOARCH=amd64 go build -trimpath -buildvcs=false \
		-o "$stage/tinvest-snapshot${ext}" ./cmd/snapshot
	cp config.example.json README.md "$stage/"
	( cd "$OUT" && "${@:4}" )
	rm -rf "$stage"
}

# ---- Existing formats ----

echo "=== Building tar.gz / zip ==="
# Linux: tar.gz
build linux "" "tinvest-snapshot-${VERSION}-linux-amd64.tar.gz" \
	tar -czf "tinvest-snapshot-${VERSION}-linux-amd64.tar.gz" tinvest-snapshot

# Windows: zip if available, otherwise tar.gz
if command -v zip >/dev/null 2>&1; then
	build windows ".exe" "tinvest-snapshot-${VERSION}-windows-amd64.zip" \
		zip -qr "tinvest-snapshot-${VERSION}-windows-amd64.zip" tinvest-snapshot
else
	build windows ".exe" "tinvest-snapshot-${VERSION}-windows-amd64.tar.gz" \
		tar -czf "tinvest-snapshot-${VERSION}-windows-amd64.tar.gz" tinvest-snapshot
fi

# ---- deb package ----

build_deb() {
	local raw_version="$1"
	# Strip leading 'v' for deb version field (must start with digit)
	local version="${raw_version#v}"
	local pkg="tinvest-snapshot_${version}_amd64.deb"
	echo "=== Building $pkg ==="

	local stage="$OUT/deb-pkg"
	rm -rf "$stage"
	mkdir -p "$stage/opt/tinvest-snapshot"
	mkdir -p "$stage/usr/local/bin"
	mkdir -p "$stage/usr/share/doc/tinvest-snapshot"
	mkdir -p "$stage/usr/share/applications"
	mkdir -p "$stage/usr/share/icons/hicolor/128x128/apps"

	# Binary + symlink
	cp "$OUT/tinvest-snapshot-linux-binary" "$stage/opt/tinvest-snapshot/tinvest-snapshot"
	chmod 755 "$stage/opt/tinvest-snapshot/tinvest-snapshot"
	ln -sf /opt/tinvest-snapshot/tinvest-snapshot "$stage/usr/local/bin/tinvest-snapshot"

	# Docs
	cp config.example.json README.md "$stage/opt/tinvest-snapshot/"
	cp README.md "$stage/usr/share/doc/tinvest-snapshot/"
	gzip -cn9 CHANGELOG.md > "$stage/usr/share/doc/tinvest-snapshot/changelog.gz" 2>/dev/null || true
	local year
	year=$(date +%Y)
	cat > "$stage/usr/share/doc/tinvest-snapshot/copyright" <<COPYEOF
Format: https://www.debian.org/doc/packaging-manuals/copyright-format/1.0/
Upstream-Name: tinvest-snapshot
Upstream-Contact: https://github.com/svdmitrij/tinvest-snapshot
Files: *
Copyright: ${year} svdmitrij
License: MIT
COPYEOF

	# Desktop integration
	cp assets/tinvest-snapshot.desktop "$stage/usr/share/applications/"
	cp assets/icon_128.png "$stage/usr/share/icons/hicolor/128x128/apps/tinvest-snapshot.png"

	# Installed size in KiB
	local inst_size
	inst_size=$(du -sk "$stage" | cut -f1)

	# Control file
	local maintainer
	maintainer=$(git config user.email 2>/dev/null || echo "dev@example.com")
	mkdir -p "$stage/DEBIAN"
	cat > "$stage/DEBIAN/control" <<CTRL
Package: tinvest-snapshot
Version: ${version}
Architecture: amd64
Maintainer: ${maintainer}
Installed-Size: ${inst_size}
Section: office
Priority: optional
Depends:
Description: T-Invest portfolio snapshot utility
 CLI-утилита для получения снимка портфеля Т-Инвестиций,
 выгрузки операций и инструментов в JSON, CSV и XLSX.
 .
 Поддерживает GUI (Fyne) и консольный режим.
CTRL

	# md5sums (optional but good practice)
	( cd "$stage" && find . -path ./DEBIAN -prune -o -type f -print0 | sort -z | \
		xargs -0 md5sum | sed 's|  ./|  |' > DEBIAN/md5sums ) 2>/dev/null || true

	dpkg-deb --root-owner-group --build "$stage" "$OUT/$pkg"
	rm -rf "$stage"
}

# ---- rpm package ----

build_rpm() {
	local raw_version="$1"
	# Strip leading 'v' for rpm version field
	local version="${raw_version#v}"
	local pkg="tinvest-snapshot-${version}-1.x86_64.rpm"
	echo "=== Building $pkg ==="

	local stage="$OUT/rpm-pkg"
	rm -rf "$stage"
	mkdir -p "$stage/opt/tinvest-snapshot"
	mkdir -p "$stage/usr/local/bin"
	mkdir -p "$stage/usr/share/doc/tinvest-snapshot"
	mkdir -p "$stage/usr/share/applications"
	mkdir -p "$stage/usr/share/icons/hicolor/128x128/apps"

	# Binary + symlink
	cp "$OUT/tinvest-snapshot-linux-binary" "$stage/opt/tinvest-snapshot/tinvest-snapshot"
	chmod 755 "$stage/opt/tinvest-snapshot/tinvest-snapshot"
	ln -sf /opt/tinvest-snapshot/tinvest-snapshot "$stage/usr/local/bin/tinvest-snapshot"

	# Docs
	cp config.example.json README.md "$stage/opt/tinvest-snapshot/"
	cp README.md "$stage/usr/share/doc/tinvest-snapshot/"
	gzip -cn9 CHANGELOG.md > "$stage/usr/share/doc/tinvest-snapshot/changelog.gz" 2>/dev/null || true

	# Desktop integration
	cp assets/tinvest-snapshot.desktop "$stage/usr/share/applications/"
	cp assets/icon_128.png "$stage/usr/share/icons/hicolor/128x128/apps/tinvest-snapshot.png"

	# Build CPIO payload
	( cd "$stage" && find . | cpio --quiet -o -H newc ) > "$OUT/rpm-payload.cpio"
	local payload_size
	payload_size=$(stat -c%s "$OUT/rpm-payload.cpio")
	gzip -n9 "$OUT/rpm-payload.cpio"
	local gzpayload="$OUT/rpm-payload.cpio.gz"
	local gz_size
	gz_size=$(stat -c%s "$gzpayload")

	# Total installed size
	local inst_size
	inst_size=$(du -sk "$stage" | cut -f1)

	# Build RPM header and lead
	local rpmbin="$OUT/$pkg"
	_build_rpm_binary "$version" "$inst_size" "$payload_size" "$gz_size" "$gzpayload" "$rpmbin"

	rm -f "$gzpayload" "$OUT/rpm-payload.cpio" 2>/dev/null || true
	rm -rf "$stage"
}

# Internal: assemble binary RPM file (lead + sig header + header + payload).
# Uses Python to write the binary RPM header correctly.
_build_rpm_binary() {
	local version="$1" inst_size_kb="$2" payload_size="$3" gz_size="$4"
	local gzpayload="$5" outfile="$6"

	python3 - "$version" "$inst_size_kb" "$payload_size" "$gz_size" "$gzpayload" "$outfile" <<'PYEOF'
import struct, sys, os

version, inst_size_kb, payload_size, gz_size, gzpayload, outfile = sys.argv[1:7]
inst_size = int(inst_size_kb) * 1024
payload_size = int(payload_size)

def write_lead(f, name_ver):
    """Write RPM v3 lead (96 bytes)."""
    magic = b'\xed\xab\xee\xdb'           # RPM magic
    major = b'\x03'                        # v3
    minor = b'\x00'
    ptype  = struct.pack('>H', 0)          # 0 = binary
    archnum = struct.pack('>H', 1)         # 1 = x86_64
    name = name_ver.ljust(66, b'\x00')[:66]
    osnum = struct.pack('>H', 1)           # 1 = Linux
    sigtype = struct.pack('>H', 5)         # 5 = header-style signatures
    reserved = b'\x00' * 16
    f.write(magic + major + minor + ptype + archnum + name + osnum + sigtype + reserved)

def write_header(f, entries):
    """Write an RPM header given a list of (tag, type, value) entries."""
    # Build index and data store
    index_data = b''
    store = b''
    for tag, dtype, value in entries:
        offset = len(store)
        if dtype == 4:  # INT32
            count = 1
            data = struct.pack('>i', int(value))
        elif dtype == 6:  # STRING
            data = value.encode('utf-8') + b'\x00'
            count = 1
        elif dtype == 7:  # BIN
            data = value if isinstance(value, bytes) else value.encode('utf-8')
            count = len(data)
        elif dtype == 8:  # STRING_ARRAY
            data = b''.join((s.encode('utf-8') + b'\x00' for s in value))
            count = len(value)
        elif dtype == 9:  # I18NSTRING
            data = value.encode('utf-8') + b'\x00'
            count = 1
        else:
            raise ValueError(f'Unknown dtype {dtype}')
        index_data += struct.pack('>IIII', tag, dtype, offset, count)
        store += data

    # Header magic + reserved + index count + data size
    hdr_start = struct.pack('>I', 0x8eade801)  # magic (unsigned)
    hdr_start += struct.pack('>I', 0)           # reserved
    hdr_start += struct.pack('>I', len(entries)) # index count
    hdr_start += struct.pack('>I', len(store))   # data size
    f.write(hdr_start + index_data + store)

def write_sig_header(f):
    """Write empty signature header."""
    f.write(struct.pack('>I', 0x8eade801))
    f.write(struct.pack('>I', 0))  # reserved
    f.write(struct.pack('>I', 0))  # index count
    f.write(struct.pack('>I', 0))  # data size

# Build RPM
with open(outfile, 'wb') as f:
    name_ver = f'tinvest-snapshot-{version}-1'.encode('utf-8')
    write_lead(f, name_ver)
    write_sig_header(f)

    entries = [
        (1000, 6, 'tinvest-snapshot'),      # NAME
        (1001, 6, version),                 # VERSION
        (1002, 6, '1'),                     # RELEASE
        (1004, 6, 'T-Invest portfolio snapshot utility'),  # SUMMARY
        (1005, 9, 'CLI-утилита для получения снимка портфеля Т-Инвестиций, выгрузки операций и инструментов в JSON, CSV и XLSX. Поддерживает GUI (Fyne) и консольный режим.'),  # DESCRIPTION
        (1009, 4, inst_size),               # SIZE (installed bytes)
        (1014, 6, 'MIT'),                   # LICENSE
        (1020, 6, 'https://github.com/svdmitrij/tinvest-snapshot'),  # URL
        (1021, 6, 'linux'),                 # OS
        (1022, 6, 'x86_64'),                # ARCH
        (1046, 4, payload_size),            # ARCHIVESIZE
        (1064, 6, '4.13.0'),                # RPMVERSION
        (1124, 6, 'cpio'),                  # PAYLOADFORMAT
        (1125, 6, 'gzip'),                  # PAYLOADCOMPRESSOR
        (1126, 6, '9'),                     # PAYLOADFLAGS
    ]
    write_header(f, entries)

    # Append gzipped cpio payload
    with open(gzpayload, 'rb') as p:
        f.write(p.read())

# Align to 8-byte boundary
sz = os.path.getsize(outfile)
if sz % 8 != 0:
    with open(outfile, 'ab') as f:
        f.write(b'\x00' * (8 - sz % 8))

print(f'  {outfile} ({os.path.getsize(outfile)} bytes)')
PYEOF
}

# ---- MSI package (wixl) ----

build_msi() {
	local version="$1"
	local pkg="tinvest-snapshot-${version}-amd64.msi"
	echo "=== Building $pkg ==="

	if ! command -v wixl >/dev/null 2>&1; then
		echo "  [SKIP] wixl not installed (msitools package). Install with: sudo apt install msitools"
		echo "  [SKIP] The Windows .exe is still available as zip/tar.gz."
		return 0
	fi

	local stage="$OUT/msi-pkg"
	rm -rf "$stage"
	mkdir -p "$stage"

	cp "$OUT/tinvest-snapshot-windows-binary" "$stage/tinvest-snapshot.exe"
	cp config.example.json README.md "$stage/"
	cp assets/icon.ico "$stage/"

	# Write WiX source
	local wix="$OUT/packaging.wxs"
	# Sanitize version for WiX (must be N.N.N[.N] — no leading 'v', no suffixes)
	local wix_version
	wix_version=$(echo "$version" | sed 's/^v//; s/-.*//')
	# Ensure at least 3 components
	case "$wix_version" in
		*.*.*) ;;
		*.*) wix_version="${wix_version}.0" ;;
		*)   wix_version="${wix_version}.0.0" ;;
	esac

	local product_id
	product_id=$(python3 -c "import uuid; print(uuid.uuid4())" 2>/dev/null || echo "FFFFFFFF-FFFF-FFFF-FFFF-FFFFFFFFFFFF")
	local upgrade_id
	upgrade_id=$(echo "tinvest-snapshot-upgrade" | python3 -c "import sys,uuid; print(uuid.uuid5(uuid.NAMESPACE_DNS, sys.stdin.read().strip()))" 2>/dev/null || echo "AAAAAAAA-AAAA-AAAA-AAAA-AAAAAAAAAAAA")

	cat > "$wix" <<WIXEOF
<?xml version="1.0" encoding="utf-8"?>
<Wix xmlns="http://schemas.microsoft.com/wix/2006/wi">
  <Product
    Id="${product_id}"
    Name="T-Invest Snapshot"
    Language="1049"
    Version="${wix_version}"
    Manufacturer="svdmitrij"
    UpgradeCode="${upgrade_id}">

    <Package
      InstallerVersion="200"
      Compressed="yes"
      InstallScope="perMachine"
      Platform="x64" />

    <MajorUpgrade DowngradeErrorMessage="A newer version is already installed." />
    <MediaTemplate EmbedCab="yes" />

    <Property Id="ARPPRODUCTICON" Value="icon.ico" />

    <Directory Id="TARGETDIR" Name="SourceDir">
      <Directory Id="ProgramFiles64Folder">
        <Directory Id="INSTALLFOLDER" Name="tinvest-snapshot">
          <Component Id="MainExecutable" Guid="$(python3 -c "import uuid; print(uuid.uuid4())" 2>/dev/null || echo "BBBBBBBB-BBBB-BBBB-BBBB-BBBBBBBBBBBB")">
            <File Id="ExeFile" Name="tinvest-snapshot.exe" Source="${stage}/tinvest-snapshot.exe" KeyPath="yes" />
            <File Id="ConfigFile" Name="config.example.json" Source="${stage}/config.example.json" />
            <File Id="ReadmeFile" Name="README.md" Source="${stage}/README.md" />
            <File Id="IconFile" Name="icon.ico" Source="${stage}/icon.ico" />
          </Component>
          <Component Id="EnvPath" Guid="$(python3 -c "import uuid; print(uuid.uuid4())" 2>/dev/null || echo "CCCCCCCC-CCCC-CCCC-CCCC-CCCCCCCCCCCC")">
            <Environment Id="PathEnv" Name="PATH" Action="set" Part="last" System="no" Value="[INSTALLFOLDER]" />
          </Component>
        </Directory>
      </Directory>
      <Directory Id="ProgramMenuFolder">
        <Directory Id="OfficeMenuFolder" Name="Офис">
          <Component Id="StartMenuShortcut" Guid="$(python3 -c "import uuid; print(uuid.uuid4())" 2>/dev/null || echo "DDDDDDDD-DDDD-DDDD-DDDD-DDDDDDDDDDDD")">
            <Shortcut Id="StartMenuLink"
              Name="T-Invest Snapshot"
              Description="Снимок портфеля Т-Инвестиций"
              Target="[INSTALLFOLDER]tinvest-snapshot.exe"
              WorkingDirectory="INSTALLFOLDER"
              Icon="icon.ico" />
            <RemoveFolder Id="RemoveOfficeMenuFolder" On="uninstall" />
          </Component>
        </Directory>
      </Directory>
    </Directory>

    <Feature Id="Complete" Level="1" Title="T-Invest Snapshot" Display="expand">
      <ComponentRef Id="MainExecutable" />
      <ComponentRef Id="EnvPath" />
      <ComponentRef Id="StartMenuShortcut" />
    </Feature>

    <UIRef Id="WixUI_InstallDir" />
    <Property Id="WIXUI_INSTALLDIR" Value="INSTALLFOLDER" />
  </Product>
</Wix>
WIXEOF

	# Validate WiX version — wixl requires exactly 4-part version
	if [[ ! "$wix_version" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
		# Pad to 4 parts
		local dots
		dots=$(echo "$wix_version" | tr -cd '.' | wc -c)
		while [ "$dots" -lt 3 ]; do
			wix_version="${wix_version}.0"
			dots=$((dots + 1))
		done
	fi

	# wixl requires -a for x64, -o for output, .wxs input
	if ! wixl -a x64 -o "$OUT/$pkg" "$wix" 2>&1; then
		echo "  [SKIP] wixl failed to build MSI. The .exe is still available as zip/tar.gz."
		rm -f "$wix"
		rm -rf "$stage"
		return 0
	fi

	rm -f "$wix"
	rm -rf "$stage"
	echo "  $OUT/$pkg"
}

# ---- Main build flow ----

# Build Linux binary once (used by tar.gz, deb, rpm)
echo "=== Building Linux binary ==="
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -buildvcs=false \
	-o "$OUT/tinvest-snapshot-linux-binary" ./cmd/snapshot

# Build Windows binary once (used by zip, msi)
echo "=== Building Windows binary ==="
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -buildvcs=false \
	-o "$OUT/tinvest-snapshot-windows-binary" ./cmd/snapshot
mv "$OUT/tinvest-snapshot-windows-binary" "$OUT/tinvest-snapshot-windows-binary.exe" 2>/dev/null || true

# Build all package formats
build_deb "$VERSION"
build_rpm "$VERSION"
build_msi "$VERSION"

# Also build the existing tar.gz/zip from the pre-built binaries
echo "=== Packaging tar.gz / zip ==="
mkdir -p "$OUT/tinvest-snapshot"
cp "$OUT/tinvest-snapshot-linux-binary" "$OUT/tinvest-snapshot/tinvest-snapshot"
cp config.example.json README.md "$OUT/tinvest-snapshot/"
( cd "$OUT" && tar -czf "tinvest-snapshot-${VERSION}-linux-amd64.tar.gz" tinvest-snapshot )
rm -rf "$OUT/tinvest-snapshot"

mkdir -p "$OUT/tinvest-snapshot"
cp "$OUT/tinvest-snapshot-windows-binary.exe" "$OUT/tinvest-snapshot/tinvest-snapshot.exe" 2>/dev/null || true
cp config.example.json README.md "$OUT/tinvest-snapshot/"
if command -v zip >/dev/null 2>&1; then
	( cd "$OUT" && zip -qr "tinvest-snapshot-${VERSION}-windows-amd64.zip" tinvest-snapshot )
else
	( cd "$OUT" && tar -czf "tinvest-snapshot-${VERSION}-windows-amd64.tar.gz" tinvest-snapshot )
fi
rm -rf "$OUT/tinvest-snapshot"

# Clean up temporary binaries
rm -f "$OUT/tinvest-snapshot-linux-binary" "$OUT/tinvest-snapshot-windows-binary.exe"

# SHA256SUMS
( cd "$OUT" && sha256sum tinvest-snapshot* > SHA256SUMS.txt )

echo ""
echo "Release artifacts (version ${VERSION}):"
ls -la "$OUT"
