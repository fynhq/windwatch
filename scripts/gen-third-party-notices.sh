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

emit_text() {
  label=$1
  printf '\n## %s\n\n' "$label"
  cat
}

emit "WindWatch" LICENSE
emit_text "Go standard library" <<'EOF'
Copyright 2009 The Go Authors.

Redistribution and use in source and binary forms, with or without
modification, are permitted provided that the following conditions are
met:

   * Redistributions of source code must retain the above copyright
notice, this list of conditions and the following disclaimer.
   * Redistributions in binary form must reproduce the above
copyright notice, this list of conditions and the following disclaimer
in the documentation and/or other materials provided with the
distribution.
   * Neither the name of Google LLC nor the names of its
contributors may be used to endorse or promote products derived from
this software without specific prior written permission.

THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS
"AS IS" AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT
LIMITED TO, THE IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR
A PARTICULAR PURPOSE ARE DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT
OWNER OR CONTRIBUTORS BE LIABLE FOR ANY DIRECT, INDIRECT, INCIDENTAL,
SPECIAL, EXEMPLARY, OR CONSEQUENTIAL DAMAGES (INCLUDING, BUT NOT
LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR SERVICES; LOSS OF USE,
DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER CAUSED AND ON ANY
THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY, OR TORT
(INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE
OF THIS SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.
EOF
emit_text "Go patent grant" <<'EOF'
Additional IP Rights Grant (Patents)

"This implementation" means the copyrightable works distributed by
Google as part of the Go project.

Google hereby grants to You a perpetual, worldwide, non-exclusive,
no-charge, royalty-free, irrevocable (except as stated in this section)
patent license to make, have made, use, offer to sell, sell, import,
transfer and otherwise run, modify and propagate the contents of this
implementation of Go, where such license applies only to those patent
claims, both currently owned or controlled by Google and acquired in
the future, licensable by Google that are necessarily infringed by this
implementation of Go.  This grant does not include claims that would be
infringed only as a consequence of further modification of this
implementation.  If you or your agent or exclusive licensee institute or
order or agree to the institution of patent litigation against any
entity (including a cross-claim or counterclaim in a lawsuit) alleging
that this implementation of Go or any code incorporated within this
implementation of Go constitutes direct or contributory patent
infringement, or inducement of patent infringement, then any patent
rights granted to you under this License for this implementation of Go
shall terminate as of the date such litigation is filed.
EOF

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
