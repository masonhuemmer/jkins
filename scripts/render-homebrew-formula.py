#!/usr/bin/env python3
"""Render the Homebrew formula from the archives built by GoReleaser."""

import hashlib
import pathlib
import re
import sys
import textwrap


def main() -> None:
    if len(sys.argv) != 3 or not re.fullmatch(r"\d+\.\d+\.\d+", sys.argv[1]):
        raise SystemExit("usage: render-homebrew-formula.py VERSION DIST_DIR")
    version = sys.argv[1]
    dist = pathlib.Path(sys.argv[2])

    def archive(os_name: str, arch: str) -> tuple[str, str]:
        name = f"jkins_{version}_{os_name}_{arch}.tar.gz"
        digest = hashlib.sha256((dist / name).read_bytes()).hexdigest()
        return name, digest

    mac_arm, mac_arm_sha = archive("darwin", "arm64")
    mac_amd, mac_amd_sha = archive("darwin", "amd64")
    linux_arm, linux_arm_sha = archive("linux", "arm64")
    linux_amd, linux_amd_sha = archive("linux", "amd64")
    base = f"https://github.com/masonhuemmer/jkins/releases/download/v{version}"
    formula = textwrap.dedent(
        f'''\
        class Jkins < Formula
          desc "Native Go Jenkins CLI with an encrypted local vault"
          homepage "https://github.com/masonhuemmer/jkins"
          version "{version}"
          license "MIT"

          on_macos do
            if Hardware::CPU.arm?
              url "{base}/{mac_arm}"
              sha256 "{mac_arm_sha}"
            else
              url "{base}/{mac_amd}"
              sha256 "{mac_amd_sha}"
            end
          end

          on_linux do
            if Hardware::CPU.arm?
              url "{base}/{linux_arm}"
              sha256 "{linux_arm_sha}"
            else
              url "{base}/{linux_amd}"
              sha256 "{linux_amd_sha}"
            end
          end

          def install
            bin.install "jkins"
          end

          test do
            assert_match "jkins v#{{version}}", shell_output("#{{bin}}/jkins --version")
          end
        end
        '''
    )
    target = dist / "homebrew" / "Formula" / "jkins.rb"
    target.parent.mkdir(parents=True, exist_ok=True)
    target.write_text(formula)


if __name__ == "__main__":
    main()
