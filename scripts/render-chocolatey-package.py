#!/usr/bin/env python3
"""Render a Chocolatey package from the published Windows release archives."""

import hashlib
import pathlib
import re
import sys
import textwrap
import zipfile


def main() -> None:
    if len(sys.argv) != 3 or not re.fullmatch(r"\d+\.\d+\.\d+", sys.argv[1]):
        raise SystemExit("usage: render-chocolatey-package.py VERSION DIST_DIR")
    version = sys.argv[1]
    dist = pathlib.Path(sys.argv[2])

    def archive(arch: str) -> tuple[str, str]:
        name = f"jkins_{version}_windows_{arch}.zip"
        path = dist / name
        with zipfile.ZipFile(path) as package:
            if set(package.namelist()) != {"LICENSE", "README.md", "jkins.exe"} or len(package.namelist()) != 3:
                raise ValueError(f"unexpected contents in {name}")
        return name, hashlib.sha256(path.read_bytes()).hexdigest()

    amd_name, amd_sha = archive("amd64")
    arm_name, arm_sha = archive("arm64")
    base = f"https://github.com/masonhuemmer/jkins/releases/download/v{version}"
    package_dir = dist / "chocolatey"
    tools_dir = package_dir / "tools"
    tools_dir.mkdir(parents=True, exist_ok=True)
    (package_dir / "jkins.nuspec").write_text(
        textwrap.dedent(
            f'''\
            <?xml version="1.0" encoding="utf-8"?>
            <package xmlns="http://schemas.microsoft.com/packaging/2015/06/nuspec.xsd">
              <metadata>
                <id>jkins</id>
                <version>{version}</version>
                <title>jkins</title>
                <authors>Jacob Huemmer</authors>
                <owners>masonhuemmer</owners>
                <projectUrl>https://github.com/masonhuemmer/jkins</projectUrl>
                <packageSourceUrl>https://github.com/masonhuemmer/jkins/blob/main/scripts/render-chocolatey-package.py</packageSourceUrl>
                <licenseUrl>https://github.com/masonhuemmer/jkins/blob/main/LICENSE</licenseUrl>
                <requireLicenseAcceptance>false</requireLicenseAcceptance>
                <summary>Native Go Jenkins CLI with an encrypted local vault</summary>
                <description>jkins runs Jenkins CLI commands directly over the WebSocket protocol and stores API credentials in an encrypted local vault.</description>
                <releaseNotes>https://github.com/masonhuemmer/jkins/releases/tag/v{version}</releaseNotes>
                <copyright>Copyright (c) 2026 Jacob Huemmer</copyright>
                <tags>jenkins cli ci cd</tags>
              </metadata>
              <files>
                <file src="tools\\**" target="tools" />
              </files>
            </package>
            '''
        ),
        encoding="utf-8",
    )
    (tools_dir / "chocolateyInstall.ps1").write_text(
        textwrap.dedent(
            f'''\
            $ErrorActionPreference = 'Stop'
            $toolsDir = Split-Path -Parent $MyInvocation.MyCommand.Definition
            $arch = $env:PROCESSOR_ARCHITEW6432
            if (-not $arch) {{ $arch = $env:PROCESSOR_ARCHITECTURE }}

            if ($arch -eq 'ARM64') {{
              Install-ChocolateyZipPackage -PackageName 'jkins' -Url '{base}/{arm_name}' -UnzipLocation $toolsDir -Checksum '{arm_sha}' -ChecksumType 'sha256'
            }} elseif ($arch -eq 'AMD64') {{
              Install-ChocolateyZipPackage -PackageName 'jkins' -Url '{base}/{amd_name}' -UnzipLocation $toolsDir -Checksum '{amd_sha}' -ChecksumType 'sha256'
            }} else {{
              throw "jkins supports 64-bit x64 and ARM64 Windows; detected $arch"
            }}
            '''
        ),
        encoding="utf-8",
    )


if __name__ == "__main__":
    main()
