#!/bin/sh
set -eu

export LC_ALL=C
cd "$(dirname "$0")/.."

main_module="github.com/fynhq/windwatch"

cat <<'EOF'
# Third-Party Notices

WindWatch binaries include the Go standard library and the third-party modules
listed below. This file is generated from the packages compiled into
`./cmd/windwatch`; do not edit it by hand. Regenerate it with `make notices`.

WindWatch itself is licensed under the MIT License.
EOF

modules=$(
  go list -deps -f '{{if not .Standard}}{{with .Module}}{{.Path}}@{{.Version}}{{end}}{{end}}' ./cmd/windwatch |
    sed '/^$/d' |
    sort -u
)

runtime_modules=""
for module in $modules; do
  case "$module" in
    "${main_module}@"|"${main_module}") continue ;;
    *@?*) ;;
    *) continue ;;
  esac
  if [ -z "$runtime_modules" ]; then
    runtime_modules=$module
  else
    runtime_modules="$runtime_modules
$module"
  fi
done

printf '## Included modules\n\n'
for module in $runtime_modules; do
  printf -- '- `%s`\n' "$module"
done

emit() {
  label=$1
  file=$2
  if [ ! -s "$file" ]; then
    return
  fi
  printf '\n## %s\n\n' "$label"
  cat "$file"
}

emit "WindWatch" LICENSE
emit "Go standard library" third_party/go-LICENSE
emit "Go patent grant" third_party/go-PATENTS

for module in $runtime_modules; do
  path=${module%@*}
  version=${module##*@}
  dir=$(go list -m -f '{{.Dir}}' "$path")
  if [ -z "$dir" ] || [ ! -d "$dir" ]; then
    echo "missing module directory for $module" >&2
    exit 1
  fi
  license_files=$(
    find "$dir" -maxdepth 1 -type f \
      \( -iname 'LICENSE*' -o -iname 'COPYING*' -o -iname 'NOTICE*' \) \
      -print | sort
  )
  if [ -z "$license_files" ]; then
    echo "no license file found for $module" >&2
    exit 1
  fi
  printf '\n## %s %s\n\n' "$path" "$version"
  first_file=1
  for file in $license_files; do
    if [ ! -s "$file" ]; then
      continue
    fi
    if [ "$first_file" -eq 0 ]; then
      printf '\n'
    fi
    first_file=0
    printf '### %s\n\n' "$(basename "$file")"
    cat "$file"
  done
done
