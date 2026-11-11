# Releasing OpenPlex

Releases are built by GitHub Actions and signed offline: panels install only
releases whose `SHA256SUMS` is signed by a key in `internal/update/keys.go`
(today `avakiam-release-2026`), and that private key never reaches GitHub.

1. Make sure `main` is green (CI runs `make check` and the dashboard build).
2. Tag the release with its notes as the tag message (they are shown in every
   panel's **Updates** page):

   ```bash
   git tag -a v0.18.0 -F notes.md
   git push origin v0.18.0
   ```

3. The **Release** workflow builds `openplexd-linux-amd64`, `openplexd-linux-arm64`,
   `install.sh` and `SHA256SUMS`, and creates a **draft** release.
4. Sign and publish from the machine holding the key:

   ```bash
   GITHUB_TOKEN=… scripts/sign-release.sh v0.18.0 ~/.openplex/keys/avakiam-release-2026.key
   ```

   It downloads the draft, checks every checksum, signs `SHA256SUMS`, uploads
   `SHA256SUMS.sig` and publishes the release. Panels see it within 12 hours
   (or at once with **Check now**); `install.sh` uses it immediately.

Versions are `MAJOR.MINOR.PATCH`; only published, non-pre-release releases are
offered as updates. To rotate the release key, add the new public key to
`internal/update/keys.go` and to `scripts/install.sh`, ship a release signed
with the old key, then sign the following ones with the new key.
