// Package coremgr downloads and updates the proxy cores from GitHub releases.
package coremgr

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/pedramrj/dunvex/internal/geoip"
)

// Repo of Dunvex itself (its releases also carry a sing-box build with per-user stats).
const Repo = "pedramrj/Dunvex"

var client = &http.Client{Timeout: 10 * time.Minute}

// proxyURL lets servers with poor GitHub access use a mirror prefix, e.g. https://ghfast.top/
func proxyURL(u string) string {
	if p := os.Getenv("DUNVEX_GITHUB_PROXY"); p != "" && strings.HasPrefix(u, "https://github.com/") {
		return strings.TrimRight(p, "/") + "/" + u
	}
	return u
}

type asset struct {
	Name   string `json:"name"`
	URL    string `json:"browser_download_url"`
	Digest string `json:"digest"` // "sha256:<hex>" (GitHub adds this for new uploads)
	Size   int64  `json:"size"`
}

type release struct {
	Tag    string  `json:"tag_name"`
	Assets []asset `json:"assets"`
}

func latest(repo string) (*release, error) {
	req, _ := http.NewRequest("GET", "https://api.github.com/repos/"+repo+"/releases/latest", nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	if t := os.Getenv("GITHUB_TOKEN"); t != "" {
		req.Header.Set("Authorization", "Bearer "+t)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("GitHub API %s: %s", repo, resp.Status)
	}
	var r release
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, err
	}
	return &r, nil
}

// LatestVersion returns the newest release tag of a core ("xray" or "sing-box").
func LatestVersion(core string) (string, error) {
	repo := "XTLS/Xray-core"
	if core == "sing-box" {
		repo = "SagerNet/sing-box"
	}
	r, err := latest(repo)
	if err != nil {
		return "", err
	}
	return strings.TrimPrefix(r.Tag, "v"), nil
}

func download(a asset) ([]byte, error) {
	resp, err := client.Get(proxyURL(a.URL))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("download %s: %s", a.Name, resp.Status)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if strings.HasPrefix(a.Digest, "sha256:") {
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != strings.TrimPrefix(a.Digest, "sha256:") {
			return nil, fmt.Errorf("checksum mismatch for %s", a.Name)
		}
	}
	return data, nil
}

func goarch() string {
	return runtime.GOARCH
}

// InstallXray downloads the latest Xray-core with geo files into binDir.
func InstallXray(binDir string) (string, error) {
	r, err := latest("XTLS/Xray-core")
	if err != nil {
		return "", err
	}
	var want string
	switch runtime.GOOS + "/" + goarch() {
	case "linux/amd64":
		want = "Xray-linux-64.zip"
	case "linux/arm64":
		want = "Xray-linux-arm64-v8a.zip"
	case "linux/arm":
		want = "Xray-linux-arm32-v7a.zip"
	case "windows/amd64":
		want = "Xray-windows-64.zip"
	case "windows/arm64":
		want = "Xray-windows-arm64-v8a.zip"
	case "darwin/arm64":
		want = "Xray-macos-arm64-v8a.zip"
	case "darwin/amd64":
		want = "Xray-macos-64.zip"
	default:
		return "", fmt.Errorf("no Xray build for %s/%s", runtime.GOOS, goarch())
	}
	var zipAsset, dgst *asset
	for i := range r.Assets {
		switch r.Assets[i].Name {
		case want:
			zipAsset = &r.Assets[i]
		case want + ".dgst":
			dgst = &r.Assets[i]
		}
	}
	if zipAsset == nil {
		return "", fmt.Errorf("asset %s not found in Xray %s", want, r.Tag)
	}
	data, err := download(*zipAsset)
	if err != nil {
		return "", err
	}
	if dgst != nil && zipAsset.Digest == "" {
		if d, err := download(*dgst); err == nil {
			sum := sha256.Sum256(data)
			if !strings.Contains(strings.ToLower(string(d)), hex.EncodeToString(sum[:])) {
				return "", fmt.Errorf("checksum mismatch for %s", want)
			}
		}
	}
	names := map[string]string{"xray": exe("xray"), "xray.exe": "xray.exe", "geoip.dat": "geoip.dat", "geosite.dat": "geosite.dat"}
	if err := extractZip(data, binDir, names); err != nil {
		return "", err
	}
	return strings.TrimPrefix(r.Tag, "v"), nil
}

// InstallSingBox installs sing-box: first the Dunvex build (with per-user stats), then upstream.
func InstallSingBox(binDir string) (string, error) {
	name := fmt.Sprintf("sing-box-%s-%s", runtime.GOOS, goarch())
	if r, err := latest(Repo); err == nil {
		for _, a := range r.Assets {
			if a.Name == name+".tar.gz" || a.Name == name+".zip" {
				data, err := download(a)
				if err != nil {
					break
				}
				target := map[string]string{"sing-box": exe("sing-box"), "sing-box.exe": "sing-box.exe"}
				if strings.HasSuffix(a.Name, ".zip") {
					err = extractZip(data, binDir, target)
				} else {
					err = extractTarGz(data, binDir, target)
				}
				if err == nil {
					return "dunvex-build", nil
				}
				break
			}
		}
	}
	r, err := latest("SagerNet/sing-box")
	if err != nil {
		return "", err
	}
	ver := strings.TrimPrefix(r.Tag, "v")
	want := fmt.Sprintf("sing-box-%s-%s-%s", ver, runtime.GOOS, goarch())
	for _, a := range r.Assets {
		if a.Name == want+".tar.gz" || a.Name == want+".zip" {
			data, err := download(a)
			if err != nil {
				return "", err
			}
			target := map[string]string{"sing-box": exe("sing-box"), "sing-box.exe": "sing-box.exe"}
			if strings.HasSuffix(a.Name, ".zip") {
				err = extractZip(data, binDir, target)
			} else {
				err = extractTarGz(data, binDir, target)
			}
			return ver, err
		}
	}
	return "", fmt.Errorf("no sing-box build for %s/%s", runtime.GOOS, goarch())
}

func exe(n string) string {
	if runtime.GOOS == "windows" {
		return n + ".exe"
	}
	return n
}

// writeAtomic writes a file next to the target and renames it in place.
func writeAtomic(path string, data []byte, mode os.FileMode) error {
	tmp := path + ".new"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		os.Remove(path)
	}
	return os.Rename(tmp, path)
}

func extractZip(data []byte, dir string, names map[string]string) error {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return err
	}
	os.MkdirAll(dir, 0o755)
	found := 0
	for _, f := range zr.File {
		base := filepath.Base(f.Name)
		out, ok := names[base]
		if !ok || f.FileInfo().IsDir() {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		b, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return err
		}
		if err := writeAtomic(filepath.Join(dir, out), b, 0o755); err != nil {
			return err
		}
		found++
	}
	if found == 0 {
		return fmt.Errorf("archive did not contain the expected files")
	}
	return nil
}

func extractTarGz(data []byte, dir string, names map[string]string) error {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	os.MkdirAll(dir, 0o755)
	found := 0
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		out, ok := names[filepath.Base(h.Name)]
		if !ok || h.Typeflag != tar.TypeReg {
			continue
		}
		b, err := io.ReadAll(tr)
		if err != nil {
			return err
		}
		if err := writeAtomic(filepath.Join(dir, out), b, 0o755); err != nil {
			return err
		}
		found++
	}
	if found == 0 {
		return fmt.Errorf("archive did not contain the expected files")
	}
	return nil
}

// LatestDunvex returns the newest Dunvex release tag.
func LatestDunvex() (string, error) {
	r, err := latest(Repo)
	if err != nil {
		return "", err
	}
	return r.Tag, nil
}

// UpdateSelf downloads the latest Dunvex binary over the running executable.
func UpdateSelf() (string, error) {
	r, err := latest(Repo)
	if err != nil {
		return "", err
	}
	want := fmt.Sprintf("dunvex-%s-%s.tar.gz", runtime.GOOS, goarch())
	for _, a := range r.Assets {
		if a.Name != want {
			continue
		}
		data, err := download(a)
		if err != nil {
			return "", err
		}
		self, err := os.Executable()
		if err != nil {
			return "", err
		}
		if err := extractTarGz(data, filepath.Dir(self), map[string]string{"dunvex": filepath.Base(self)}); err != nil {
			return "", err
		}
		return r.Tag, nil
	}
	return "", fmt.Errorf("asset %s not found in %s", want, r.Tag)
}

// GeoIPFile is the offline country data, kept in the Dunvex home folder.
const GeoIPFile = "geoip-country.bin.gz"

// InstallGeoIP downloads the country data published with the latest Dunvex
// release (built from DB-IP's free database) into dir, checking it first.
func InstallGeoIP(dir string) (string, error) {
	r, err := latest(Repo)
	if err != nil {
		return "", err
	}
	for _, a := range r.Assets {
		if a.Name != GeoIPFile {
			continue
		}
		data, err := download(a)
		if err != nil {
			return "", err
		}
		db, err := geoip.Read(bytes.NewReader(data))
		if err != nil {
			return "", fmt.Errorf("%s: %w", a.Name, err)
		}
		tmp := filepath.Join(dir, GeoIPFile+".new")
		if err := os.WriteFile(tmp, data, 0o644); err != nil {
			return "", err
		}
		if err := os.Rename(tmp, filepath.Join(dir, GeoIPFile)); err != nil {
			return "", err
		}
		src := db.Source
		if city, err := installGeoCity(r, dir); err == nil && city != "" {
			src += "; " + city
		}
		return src, nil
	}
	return "", fmt.Errorf("%s is not published with %s yet", GeoIPFile, r.Tag)
}

// GeoCityFile is the optional city and network data (cities on the world map,
// the network each user connects from), kept next to the country data.
const GeoCityFile = "geoip-city.bin.gz"

// installGeoCity downloads the city data when the release publishes it. It is
// optional: older releases do not have it and the map then shows countries.
func installGeoCity(r *release, dir string) (string, error) {
	for _, a := range r.Assets {
		if a.Name != GeoCityFile {
			continue
		}
		data, err := download(a)
		if err != nil {
			return "", err
		}
		db, err := geoip.ReadCity(bytes.NewReader(data))
		if err != nil {
			return "", fmt.Errorf("%s: %w", a.Name, err)
		}
		tmp := filepath.Join(dir, GeoCityFile+".new")
		if err := os.WriteFile(tmp, data, 0o644); err != nil {
			return "", err
		}
		return db.Source, os.Rename(tmp, filepath.Join(dir, GeoCityFile))
	}
	return "", nil
}

// InstallMTG downloads the latest mtg (MTProto proxy for Telegram, by
// 9seconds, MIT) into binDir. Its release archives hold one binary in a folder.
func InstallMTG(binDir string) (string, error) {
	r, err := latest("9seconds/mtg")
	if err != nil {
		return "", err
	}
	ver := strings.TrimPrefix(r.Tag, "v")
	arch := goarch()
	if arch == "arm" {
		arch = "armv7"
	}
	want := fmt.Sprintf("mtg-%s-%s-%s.tar.gz", ver, runtime.GOOS, arch)
	for _, a := range r.Assets {
		if a.Name != want {
			continue
		}
		data, err := download(a)
		if err != nil {
			return "", err
		}
		if err := extractTarGz(data, binDir, map[string]string{"mtg": exe("mtg"), "mtg.exe": "mtg.exe"}); err != nil {
			return "", err
		}
		return ver, nil
	}
	return "", fmt.Errorf("no mtg build for %s/%s in %s", runtime.GOOS, arch, r.Tag)
}

// InstallAWG downloads amneziawg-go (the AmneziaWG server, by Amnezia, MIT),
// built for Linux and published with the latest Dunvex release, into binDir.
func InstallAWG(binDir string) (string, error) {
	if runtime.GOOS != "linux" {
		return "", fmt.Errorf("AmneziaWG runs on Linux servers only")
	}
	r, err := latest(Repo)
	if err != nil {
		return "", err
	}
	want := fmt.Sprintf("amneziawg-go-linux-%s.tar.gz", goarch())
	for _, a := range r.Assets {
		if a.Name != want {
			continue
		}
		data, err := download(a)
		if err != nil {
			return "", err
		}
		if err := extractTarGz(data, binDir, map[string]string{"amneziawg-go": "amneziawg-go", "amneziawg-go.version": "amneziawg-go.version"}); err != nil {
			return "", err
		}
		v, _ := os.ReadFile(filepath.Join(binDir, "amneziawg-go.version"))
		return strings.TrimSpace(string(v)), nil
	}
	return "", fmt.Errorf("%s is not published with %s yet", want, r.Tag)
}

// InstallMita downloads mita, the Mieru server (by enfein, GPL-3.0), from the
// latest Mieru release into binDir. Its archives hold the single binary.
func InstallMita(binDir string) (string, error) {
	if runtime.GOOS != "linux" {
		return "", fmt.Errorf("mita (Mieru) runs on Linux servers only")
	}
	r, err := latest("enfein/mieru")
	if err != nil {
		return "", err
	}
	ver := strings.TrimPrefix(r.Tag, "v")
	want := fmt.Sprintf("mita_%s_linux_%s.tar.gz", ver, goarch())
	for _, a := range r.Assets {
		if a.Name != want {
			continue
		}
		data, err := download(a)
		if err != nil {
			return "", err
		}
		if err := extractTarGz(data, binDir, map[string]string{"mita": "mita"}); err != nil {
			return "", err
		}
		return ver, nil
	}
	return "", fmt.Errorf("no mita build for linux/%s in %s", goarch(), r.Tag)
}

// InstallTrustTunnel downloads the TrustTunnel endpoint (by AdGuard,
// Apache-2.0) from its latest release into binDir, as "trusttunnel".
func InstallTrustTunnel(binDir string) (string, error) {
	if runtime.GOOS != "linux" {
		return "", fmt.Errorf("the TrustTunnel endpoint runs on Linux servers only")
	}
	r, err := latest("TrustTunnel/TrustTunnel")
	if err != nil {
		return "", err
	}
	arch := map[string]string{"amd64": "x86_64", "arm64": "aarch64"}[goarch()]
	if arch == "" {
		return "", fmt.Errorf("no TrustTunnel build for linux/%s", goarch())
	}
	want := fmt.Sprintf("trusttunnel-%s-linux-%s.tar.gz", r.Tag, arch)
	for _, a := range r.Assets {
		if a.Name != want {
			continue
		}
		data, err := download(a)
		if err != nil {
			return "", err
		}
		if err := extractTarGz(data, binDir, map[string]string{"trusttunnel_endpoint": "trusttunnel"}); err != nil {
			return "", err
		}
		return strings.TrimPrefix(r.Tag, "v"), nil
	}
	return "", fmt.Errorf("%s is not in the TrustTunnel release %s", want, r.Tag)
}

// SlipstreamVersion is the Slipstream server Dunvex runs. Its builds are
// pinned by SHA-256 (from the release's GitHub digests), so a changed file is
// refused even if the release were altered.
const SlipstreamVersion = "v0.1.1"

var slipstreamSHA256 = map[string]string{
	"x86_64": "505f1809bc9feb682d2eb5716232c5aed0fcd34344ea1194b684629f1b7cbab8",
	"arm64":  "03a7e2b1fba5428f3bfee4b7a0c6ef58b044ea89b221cc8d13d72482a2bb586e",
}

// InstallSlipstream downloads the Slipstream server (by EndPositive,
// Apache-2.0, github.com/EndPositive/slipstream) into binDir. Its Linux
// builds need glibc and OpenSSL 3 (Ubuntu 22.04+, Debian 12+).
func InstallSlipstream(binDir string) (string, error) {
	if runtime.GOOS != "linux" {
		return "", fmt.Errorf("the Slipstream server runs on Linux servers only")
	}
	arch := map[string]string{"amd64": "x86_64", "arm64": "arm64"}[goarch()]
	if arch == "" {
		return "", fmt.Errorf("no Slipstream build for linux/%s", goarch())
	}
	name := fmt.Sprintf("slipstream-server-%s-linux-%s", SlipstreamVersion, arch)
	data, err := download(asset{Name: name, URL: "https://github.com/EndPositive/slipstream/releases/download/" + SlipstreamVersion + "/" + name,
		Digest: "sha256:" + slipstreamSHA256[arch]})
	if err != nil {
		return "", err
	}
	os.MkdirAll(binDir, 0o755)
	if err := writeAtomic(filepath.Join(binDir, "slipstream-server"), data, 0o755); err != nil {
		return "", err
	}
	return strings.TrimPrefix(SlipstreamVersion, "v"), nil
}

// InstallUsque downloads usque (MIT, github.com/Diniboy1123/usque), the
// open-source WARP client that runs Cloudflare's MASQUE (HTTP/3) tunnel as a
// local SOCKS proxy, into binDir. The download is checked against the
// release's published SHA-256.
func InstallUsque(binDir string) (string, error) {
	if runtime.GOOS != "linux" {
		return "", fmt.Errorf("usque (WARP over MASQUE) runs on Linux servers only")
	}
	r, err := latest("Diniboy1123/usque")
	if err != nil {
		return "", err
	}
	ver := strings.TrimPrefix(r.Tag, "v")
	want := fmt.Sprintf("usque_%s_linux_%s.zip", ver, goarch())
	var zipAsset, sums *asset
	for i := range r.Assets {
		switch r.Assets[i].Name {
		case want:
			zipAsset = &r.Assets[i]
		case "checksums.txt":
			sums = &r.Assets[i]
		}
	}
	if zipAsset == nil {
		return "", fmt.Errorf("no usque build for linux/%s in %s", goarch(), r.Tag)
	}
	data, err := download(*zipAsset)
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(zipAsset.Digest, "sha256:") {
		// no digest from GitHub: the release's own checksum list must vouch for it
		if sums == nil {
			return "", fmt.Errorf("usque %s publishes no checksum for %s", r.Tag, want)
		}
		list, err := download(*sums)
		if err != nil {
			return "", err
		}
		sum := sha256.Sum256(data)
		if !strings.Contains(string(list), hex.EncodeToString(sum[:])+"  "+want) {
			return "", fmt.Errorf("checksum mismatch for %s", want)
		}
	}
	if err := extractZip(data, binDir, map[string]string{"usque": "usque"}); err != nil {
		return "", err
	}
	return ver, nil
}

// DunvexBinary downloads the Dunvex executable of release tag for this
// server's system and returns it (checked against the release's digest).
func DunvexBinary(tag string) ([]byte, error) {
	req, _ := http.NewRequest("GET", "https://api.github.com/repos/"+Repo+"/releases/tags/"+tag, nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	if t := os.Getenv("GITHUB_TOKEN"); t != "" {
		req.Header.Set("Authorization", "Bearer "+t)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("Dunvex %s on GitHub: %s", tag, resp.Status)
	}
	var r release
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, err
	}
	want := fmt.Sprintf("dunvex-%s-%s.tar.gz", runtime.GOOS, goarch())
	for _, a := range r.Assets {
		if a.Name != want {
			continue
		}
		data, err := download(a)
		if err != nil {
			return nil, err
		}
		gz, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		tr := tar.NewReader(gz)
		for {
			h, err := tr.Next()
			if err != nil {
				break
			}
			if filepath.Base(h.Name) == "dunvex" && h.Typeflag == tar.TypeReg {
				return io.ReadAll(io.LimitReader(tr, 256<<20))
			}
		}
		return nil, fmt.Errorf("%s has no dunvex binary", want)
	}
	return nil, fmt.Errorf("%s is not in Dunvex %s", want, tag)
}
