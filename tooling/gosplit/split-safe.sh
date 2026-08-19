#!/usr/bin/env bash
# Generate a split beside a Go file, compile the package, and replace the original
# only after validation succeeds. Any failure restores the original byte-for-byte.
set -euo pipefail
if (($# < 1 || $# > 2)); then
  echo "usage: $0 path/to/file.go [max-lines]" >&2
  exit 2
fi
file=$1
max_lines=${2:-500}
root=$(git rev-parse --show-toplevel)
rel=${file#"$root/"}
dir=$(dirname "$file")
base=$(basename "$file" .go)
tmp=$(mktemp -d "$dir/.${base}.split.XXXXXX")
backup="$file.goa-original"
cleanup() { rm -rf "$tmp"; }
trap cleanup EXIT

go run ./tooling/gosplit -file "$file" -out "$tmp" -max-lines "$max_lines"
generated=()
while IFS= read -r generated_file; do generated+=("$generated_file"); done < <(find "$tmp" -maxdepth 1 -type f -name '*.go' -print | sort)
((${#generated[@]} > 0)) || { echo "splitter emitted no files" >&2; exit 1; }
# Keep original in place during generation. Move it only for the compile probe.
mv "$file" "$backup"
restore() {
  rm -f "$dir/${base}_features_"*.go
  mv "$backup" "$file"
}
trap restore ERR
cp "${generated[@]}" "$dir/"
go test -run '^$' "./${dir#./}"
rm -f "$backup"
trap - ERR
printf 'validated %s (%d generated files)\n' "$rel" "${#generated[@]}"