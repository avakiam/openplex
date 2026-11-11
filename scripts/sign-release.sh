#!/usr/bin/env bash
# Signs and publishes a draft OpenPlex release built by .github/workflows/release.yml.
#
#   GITHUB_TOKEN=... scripts/sign-release.sh v0.18.0 ~/.openplex/keys/avakiam-release-2026.key
#
# Downloads the draft's SHA256SUMS and binaries, checks them, signs SHA256SUMS
# with the release key (which never leaves this machine), uploads
# SHA256SUMS.sig and publishes the release. Needs curl, python3, sha256sum and
# Go (or OPENPLEXD=/path/to/openplexd).
set -euo pipefail
REPO="${REPO:-avakiam/openplex}"
TAG="${1:?usage: sign-release.sh vX.Y.Z KEYFILE}"
KEY="${2:?usage: sign-release.sh vX.Y.Z KEYFILE}"
: "${GITHUB_TOKEN:?set GITHUB_TOKEN (a token with contents:write on $REPO)}"
API=https://api.github.com/repos/$REPO
gh_api() { curl -fsSL -H "Authorization: Bearer $GITHUB_TOKEN" -H "Accept: application/vnd.github+json" "$@"; }
json() { python3 -c "import sys,json; d=json.load(sys.stdin); $1"; }

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

release=$(gh_api "$API/releases?per_page=30" | json "
r=[x for x in d if x['tag_name']=='$TAG']
print(json.dumps(r[0]) if r else '')")
[[ -n "$release" ]] || { echo "no release for $TAG (has the workflow finished?)" >&2; exit 1; }
id=$(json "print(d['id'])" <<<"$release")
draft=$(json "print(d['draft'])" <<<"$release")
[[ "$draft" == True ]] || { echo "$TAG is already published" >&2; exit 1; }

echo "==> downloading the draft's assets"
json "
for a in d['assets']:
    print(a['id'], a['name'])" <<<"$release" | while read -r aid name; do
  [[ "$name" == SHA256SUMS.sig ]] && continue
  gh_api -H "Accept: application/octet-stream" "$API/releases/assets/$aid" -o "$WORK/$name"
done
(cd "$WORK" && sha256sum -c --quiet SHA256SUMS) || { echo "checksums of the draft don't match" >&2; exit 1; }
for arch in amd64 arm64; do
  [[ -f "$WORK/openplexd-linux-$arch" ]] || { echo "missing openplexd-linux-$arch" >&2; exit 1; }
done

echo "==> signing SHA256SUMS"
if [[ -n "${OPENPLEXD:-}" ]]; then
  "$OPENPLEXD" release sign -key "$KEY" "$WORK/SHA256SUMS"
else
  (cd "$(dirname "$0")/.." && go run ./cmd/openplexd release sign -key "$KEY" "$WORK/SHA256SUMS")
fi

echo "==> uploading SHA256SUMS.sig"
gh_api -X POST -H "Content-Type: application/json" --data-binary @"$WORK/SHA256SUMS.sig" \
  "https://uploads.github.com/repos/$REPO/releases/$id/assets?name=SHA256SUMS.sig" >/dev/null

echo "==> publishing $TAG"
gh_api -X PATCH "$API/releases/$id" -d '{"draft":false,"make_latest":"true"}' | json "print(d['html_url'])"
