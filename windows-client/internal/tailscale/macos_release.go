package tailscale

import (
	"errors"
	"io"
	"regexp"

	"golang.org/x/net/html"
)

const maximumMacPackagePageBytes = 8 << 20

var macPackagePattern = regexp.MustCompile(`^Tailscale-((?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*))-macos\.pkg$`)

type MacRelease struct {
	Version     string
	PKGURL      string
	ChecksumURL string
}

func ParseStableMacPackagePage(raw []byte) (MacRelease, error) {
	if len(raw) == 0 || len(raw) > maximumMacPackagePageBytes {
		return MacRelease{}, errors.New("Tailscale package page is missing or too large")
	}

	tokenizer := html.NewTokenizer(newByteReader(raw))
	var best MacRelease
	var bestParts [3]int
	for {
		switch tokenizer.Next() {
		case html.ErrorToken:
			if tokenizer.Err() == io.EOF {
				if best.Version == "" {
					return MacRelease{}, errors.New("Tailscale package page has no macOS PKG")
				}
				return best, nil
			}
			return MacRelease{}, errors.New("Tailscale package page is invalid")
		case html.StartTagToken, html.SelfClosingTagToken:
			name, hasAttributes := tokenizer.TagName()
			if string(name) != "a" || !hasAttributes {
				continue
			}
			href, unique := oneHTMLAttribute(tokenizer, "href")
			if !unique {
				continue
			}
			basename := href
			if len(href) > len(StablePackagesURL) && href[:len(StablePackagesURL)] == StablePackagesURL {
				basename = href[len(StablePackagesURL):]
			}
			matches := macPackagePattern.FindStringSubmatch(basename)
			if len(matches) != 2 || (href != basename && href != StablePackagesURL+basename) {
				continue
			}
			parts, err := parseVersion(matches[1])
			if err != nil {
				continue
			}
			if best.Version == "" || versionGreater(parts, bestParts) {
				pkgURL := StablePackagesURL + basename
				best = MacRelease{Version: matches[1], PKGURL: pkgURL, ChecksumURL: pkgURL + ".sha256"}
				bestParts = parts
			}
		}
	}
}

func oneHTMLAttribute(tokenizer *html.Tokenizer, wanted string) (string, bool) {
	value := ""
	found := false
	for {
		key, attribute, more := tokenizer.TagAttr()
		if string(key) == wanted {
			if found {
				return "", false
			}
			found = true
			value = string(attribute)
		}
		if !more {
			break
		}
	}
	return value, found
}

// byteReader keeps the HTML dependency behind the exact byte slice admitted by
// the package-page size check.
type byteReader struct {
	value []byte
	index int
}

func newByteReader(value []byte) *byteReader { return &byteReader{value: value} }

func (reader *byteReader) Read(destination []byte) (int, error) {
	if reader.index >= len(reader.value) {
		return 0, io.EOF
	}
	count := copy(destination, reader.value[reader.index:])
	reader.index += count
	return count, nil
}
