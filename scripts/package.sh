#!/usr/bin/env bash
# Build release artifacts for Linux and Windows (amd64).
# Produces: tar.gz, zip, deb, rpm, msi (if tools available) + SHA256SUMS.txt.
#
# Each archive contains the binary, config.example.json, README.md.
# Native packages (deb, rpm, msi) also include desktop integration
# (desktop file, icons, Start Menu shortcuts).
#
# Usage: scripts/package.sh [VERSION] [FORMATS]
#   VERSION defaults to `git describe --tags`; FORMATS is "all" or a comma-
#   separated list of deb,rpm,msi,tar,zip.
set -euo pipefail

cd "$(dirname "$0")/.."
VERSION="${1:-$(git describe --tags --always 2>/dev/null || echo dev)}"
OUT="dist"
FORMATS="${2:-all}"
if [[ "$FORMATS" == "all" ]]; then
	FORMATS="deb,rpm,msi,tar,zip"
fi

want() { case ",$FORMATS," in *",$1,"*) return 0 ;; *) return 1 ;; esac; }
rm -rf "$OUT"
mkdir -p "$OUT"

# Generate icons from SVG source
echo "=== Generating icons ==="
go run ./cmd/icon-gen --out assets/

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

	# CLI binary + symlink
	cp "$OUT/tinvest-snapshot-linux-binary" "$stage/opt/tinvest-snapshot/tinvest-snapshot"
	chmod 755 "$stage/opt/tinvest-snapshot/tinvest-snapshot"
	ln -sf /opt/tinvest-snapshot/tinvest-snapshot "$stage/usr/local/bin/tinvest-snapshot"

	# GUI binary + symlink
	cp "$OUT/tinvest-gui-linux-amd64" "$stage/opt/tinvest-snapshot/tinvest-gui"
	chmod 755 "$stage/opt/tinvest-snapshot/tinvest-gui"
	ln -sf /opt/tinvest-snapshot/tinvest-gui "$stage/usr/local/bin/tinvest-gui"

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
Depends: libgl1, libx11-6
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
	# Strip leading 'v' and sanitize for RPM (no hyphens allowed in Version)
	local version="${raw_version#v}"
	version="${version//-/_}"
	local pkg_name="tinvest-snapshot"
	local pkg="tinvest-snapshot-${version}-1.x86_64.rpm"
	echo "=== Building $pkg ==="

	if ! command -v rpmbuild >/dev/null 2>&1; then
		echo "  [SKIP] rpmbuild not installed. Install with: sudo apt install rpm"
		return 0
	fi

	local abs_out
	abs_out=$(cd "$OUT" && pwd)
	local abs_src
	abs_src=$(pwd)
	local rpmbuild_root="$abs_out/rpmbuild"
	rm -rf "$rpmbuild_root"
	mkdir -p "$rpmbuild_root"/{BUILD,RPMS,SOURCES,SPECS,SRPMS}

	# Write spec file with install section
	cat > "$rpmbuild_root/SPECS/tinvest-snapshot.spec" <<SPEC
Name:           tinvest-snapshot
Version:        ${version}
Release:        1%{?dist}
Summary:        T-Invest portfolio snapshot utility
License:        MIT
URL:            https://github.com/svdmitrij/tinvest-snapshot
BuildArch:      x86_64
AutoReqProv:    no
Requires:       libglvnd-glx, libX11

%description
CLI-утилита для получения снимка портфеля Т-Инвестиций,
выгрузки операций и инструментов в JSON, CSV и XLSX.
Поддерживает GUI (Fyne) и консольный режим.

%install
mkdir -p %{buildroot}/opt/tinvest-snapshot
mkdir -p %{buildroot}/usr/local/bin
mkdir -p %{buildroot}/usr/share/doc/tinvest-snapshot
mkdir -p %{buildroot}/usr/share/applications
mkdir -p %{buildroot}/usr/share/icons/hicolor/128x128/apps

cp ${abs_out}/tinvest-snapshot-linux-binary %{buildroot}/opt/tinvest-snapshot/tinvest-snapshot
chmod 755 %{buildroot}/opt/tinvest-snapshot/tinvest-snapshot
ln -sf /opt/tinvest-snapshot/tinvest-snapshot %{buildroot}/usr/local/bin/tinvest-snapshot
cp ${abs_out}/tinvest-gui-linux-amd64 %{buildroot}/opt/tinvest-snapshot/tinvest-gui
chmod 755 %{buildroot}/opt/tinvest-snapshot/tinvest-gui
ln -sf /opt/tinvest-snapshot/tinvest-gui %{buildroot}/usr/local/bin/tinvest-gui
cp ${abs_src}/config.example.json %{buildroot}/opt/tinvest-snapshot/
cp ${abs_src}/README.md %{buildroot}/opt/tinvest-snapshot/
cp ${abs_src}/README.md %{buildroot}/usr/share/doc/tinvest-snapshot/
gzip -cn9 ${abs_src}/CHANGELOG.md > %{buildroot}/usr/share/doc/tinvest-snapshot/changelog.gz 2>/dev/null || true
cp ${abs_src}/assets/tinvest-snapshot.desktop %{buildroot}/usr/share/applications/
cp ${abs_src}/assets/icon_128.png %{buildroot}/usr/share/icons/hicolor/128x128/apps/tinvest-snapshot.png

%files
/opt/tinvest-snapshot/tinvest-snapshot
/opt/tinvest-snapshot/tinvest-gui
/opt/tinvest-snapshot/config.example.json
/opt/tinvest-snapshot/README.md
/usr/local/bin/tinvest-snapshot
/usr/local/bin/tinvest-gui
/usr/share/doc/tinvest-snapshot/README.md
/usr/share/doc/tinvest-snapshot/changelog.gz
/usr/share/applications/tinvest-snapshot.desktop
/usr/share/icons/hicolor/128x128/apps/tinvest-snapshot.png
SPEC

	# Build RPM using rpmbuild
	if rpmbuild -bb --define "_topdir $rpmbuild_root" \
		--define "_rpmfilename %%{NAME}-%%{VERSION}-%%{RELEASE}.%%{ARCH}.rpm" \
		"$rpmbuild_root/SPECS/tinvest-snapshot.spec" 2>&1; then
		# Copy RPM to dist
		local built_rpm
		built_rpm=$(find "$rpmbuild_root/RPMS" -name "*.rpm" | head -1)
		if [ -n "$built_rpm" ] && [ -f "$built_rpm" ]; then
			cp "$built_rpm" "$OUT/$pkg"
			echo "  $OUT/$pkg"
		else
			echo "  [ERROR] rpmbuild succeeded but no RPM found"
			return 1
		fi
	else
		echo "  [ERROR] rpmbuild failed"
		return 1
	fi

	rm -rf "$rpmbuild_root"
}

# (removed _build_rpm_binary — rpmbuild handles binary assembly)

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

	if [[ ! -f "$OUT/tinvest-snapshot-windows-binary.exe" || ! -f "$OUT/tinvest-gui-windows-amd64.exe" ]]; then
		echo "  [SKIP] Windows binaries not built (mingw-w64 unavailable) - MSI skipped."
		return 0
	fi

	local stage="$OUT/msi-pkg"
	rm -rf "$stage"
	mkdir -p "$stage"

	cp "$OUT/tinvest-snapshot-windows-binary.exe" "$stage/tinvest-snapshot.exe"
	cp "$OUT/tinvest-gui-windows-amd64.exe" "$stage/tinvest-gui.exe"
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
       InstallScope="perMachine" />

    <MajorUpgrade DowngradeErrorMessage="A newer version is already installed." />
    <MediaTemplate EmbedCab="yes" />

    <Property Id="ARPPRODUCTICON" Value="icon.ico" />

    <Directory Id="TARGETDIR" Name="SourceDir">
      <Directory Id="ProgramFiles64Folder">
        <Directory Id="INSTALLFOLDER" Name="tinvest-snapshot">
           <Component Id="MainExecutable" Guid="$(python3 -c "import uuid; print(uuid.uuid4())" 2>/dev/null || echo "BBBBBBBB-BBBB-BBBB-BBBB-BBBBBBBBBBBB")">
            <File Id="GuiFile" Name="tinvest-gui.exe" Source="${stage}/tinvest-gui.exe" KeyPath="yes" />
            <File Id="ExeFile" Name="tinvest-snapshot.exe" Source="${stage}/tinvest-snapshot.exe" />
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
                             Description="Терминал Т-Инвестиций"
              Target="[INSTALLFOLDER]tinvest-gui.exe"
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

# Build GUI binaries (required for native packages)
echo "=== Building GUI binaries ==="
bash scripts/build-gui.sh linux
echo "=== Building Windows GUI (cross-compile, optional) ==="
bash scripts/build-gui.sh windows 2>&1 || echo "  [SKIP] Windows GUI: mingw-w64 not available. MSI will not be built."

# Build the requested package formats
if want deb; then build_deb "$VERSION"; fi
if want rpm; then build_rpm "$VERSION"; fi
if want msi; then build_msi "$VERSION"; fi

# Also build the requested archives from the pre-built binaries
if want tar; then
	echo "=== Building Linux archive ==="
	mkdir -p "$OUT/tinvest-snapshot"
	cp "$OUT/tinvest-snapshot-linux-binary" "$OUT/tinvest-snapshot/tinvest-snapshot"
	cp config.example.json README.md "$OUT/tinvest-snapshot/"
	( cd "$OUT" && tar -czf "tinvest-snapshot-${VERSION}-linux-amd64.tar.gz" tinvest-snapshot )
	rm -rf "$OUT/tinvest-snapshot"
fi

if want zip; then
	echo "=== Building Windows archive ==="
	mkdir -p "$OUT/tinvest-snapshot"
	cp "$OUT/tinvest-snapshot-windows-binary.exe" "$OUT/tinvest-snapshot/tinvest-snapshot.exe" 2>/dev/null || true
	cp config.example.json README.md "$OUT/tinvest-snapshot/"
	if command -v zip >/dev/null 2>&1; then
		( cd "$OUT" && zip -qr "tinvest-snapshot-${VERSION}-windows-amd64.zip" tinvest-snapshot )
	else
		( cd "$OUT" && tar -czf "tinvest-snapshot-${VERSION}-windows-amd64.tar.gz" tinvest-snapshot )
	fi
	rm -rf "$OUT/tinvest-snapshot"
fi

# Clean up temporary binaries
rm -f "$OUT/tinvest-snapshot-linux-binary" "$OUT/tinvest-snapshot-windows-binary.exe"
rm -f "$OUT/tinvest-gui-linux-amd64" "$OUT/tinvest-gui-windows-amd64.exe"

# SHA256SUMS (only if artifacts were produced)
if ls "$OUT"/tinvest-snapshot* >/dev/null 2>&1; then
	( cd "$OUT" && sha256sum tinvest-snapshot* > SHA256SUMS.txt )
else
	echo "No release artifacts produced - nothing to checksum."
fi

echo ""
echo "Release artifacts (version ${VERSION}):"
ls -la "$OUT"
