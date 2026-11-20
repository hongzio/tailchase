#!/usr/bin/env bash
# Publish already-built assets without replacing any existing release files.
set -euo pipefail
source_repo="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$source_repo"
repo="hongzio/tailchase"
version="$(ruby -rjson -e 'puts JSON.parse(File.read(ARGV[0])).fetch("version")' packaging/release.json)"
revision="$(ruby -rjson -e 'puts JSON.parse(File.read(ARGV[0])).fetch("revision")' packaging/release.json)"
if [[ ! "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ || ! "$revision" =~ ^[0-9a-f]{40}$ ]]; then
  echo 'Invalid release version or revision.' >&2; exit 1
fi
tag="v$version"
remote_revision="$(git ls-remote origin "refs/tags/$tag" "refs/tags/$tag^{}" | awk '{print $1}' | tail -n 1)"
if [[ "$remote_revision" != "$revision" ]]; then
  echo "Remote tag $tag must point to $revision before publishing." >&2; exit 1
fi
ruby -rjson -e 'config=JSON.parse(File.read(ARGV[0])); manifest=JSON.parse(File.read(ARGV[1])); abort "Build manifest does not match release config" unless config.all? { |k,v| manifest[k] == v }' packaging/release.json dist/release-manifest.json
(cd dist && shasum -a 256 -c checksums.txt)
files=("dist/tailchase_${version}_darwin_arm64.tar.gz" "dist/tailchase_${version}_darwin_amd64.tar.gz" "dist/tailchase_${version}_linux_arm64.tar.gz" "dist/tailchase_${version}_linux_amd64.tar.gz" dist/checksums.txt)
for path in "${files[@]}"; do [[ -f "$path" ]] || { echo "Missing release asset: $path" >&2; exit 1; }; done
if ! gh release view "$tag" --repo "$repo" >/dev/null 2>&1; then
  gh release create "$tag" --repo "$repo" --verify-tag --draft --title "$tag" \
    --notes 'Prebuilt tailchase executables for macOS and Linux (arm64 and amd64). Homebrew installs these binaries without a Go build dependency. The tailcat runtime is required.'
fi
existing="$(gh release view "$tag" --repo "$repo" --json assets --jq '.assets[].name')"
for path in "${files[@]}"; do
  name="${path##*/}"; found=false
  while IFS= read -r asset; do if [[ "$asset" == "$name" ]]; then found=true; break; fi; done <<< "$existing"
  if [[ "$found" == false ]]; then gh release upload "$tag" "$path" --repo "$repo"; fi
done
verify_dir="$(mktemp -d)"
trap 'rm -rf "$verify_dir"' EXIT
gh release download "$tag" --repo "$repo" --dir "$verify_dir" \
  --pattern "tailchase_${version}_*.tar.gz" --pattern checksums.txt
for path in "${files[@]}"; do
  if ! cmp -s "$path" "$verify_dir/${path##*/}"; then
    echo "Existing release asset differs: ${path##*/}; refusing to overwrite it. Use a new release version." >&2; exit 1
  fi
done
if [[ "$(gh release view "$tag" --repo "$repo" --json isDraft --jq .isDraft)" == true ]]; then
  gh release edit "$tag" --repo "$repo" --draft=false
fi
printf 'Published and verified binary assets for %s.\n' "$tag"
