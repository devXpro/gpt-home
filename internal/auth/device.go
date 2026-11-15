package auth

import (
	"strings"

	"github.com/google/uuid"
)

// browsers and platforms are checked in order: the first marker found in the User-Agent wins. Order matters,
// because Edge, Opera and Yandex also say "Chrome", and every iOS browser also says "Safari".
var (
	browsers = []struct{ marker, name string }{
		{"EdgA/", "Edge"}, {"EdgiOS/", "Edge"}, {"Edg/", "Edge"},
		{"OPR/", "Opera"}, {"YaBrowser/", "Yandex Browser"}, {"SamsungBrowser/", "Samsung Internet"},
		{"FxiOS/", "Firefox"}, {"Firefox/", "Firefox"},
		{"CriOS/", "Chrome"}, {"Chrome/", "Chrome"}, {"Chromium/", "Chrome"},
		{"Safari/", "Safari"},
	}
	platforms = []struct{ marker, name string }{
		{"iPhone", "iPhone"}, {"iPad", "iPad"}, {"Android", "Android"}, {"CrOS", "ChromeOS"},
		{"Macintosh", "Mac"}, {"Mac OS X", "Mac"}, {"Windows", "Windows"}, {"Linux", "Linux"},
	}
)

func firstMarker(ua string, list []struct{ marker, name string }) string {
	for _, m := range list {
		if strings.Contains(ua, m.marker) {
			return m.name
		}
	}
	return ""
}

// deviceLabel is the short session label of ADMIN-API §2, e.g. "Safari · iPhone".
func deviceLabel(ua string) string {
	browser, platform := firstMarker(ua, browsers), firstMarker(ua, platforms)
	switch {
	case browser == "" && platform == "":
		return "Browser"
	case browser == "":
		return platform
	case platform == "":
		return browser
	}
	return browser + " · " + platform
}

// defaultPasskeyName names a passkey after the platform that made it when the client sent no name.
func defaultPasskeyName(ua string) string {
	if p := firstMarker(ua, platforms); p != "" {
		return p
	}
	return "Passkey"
}

// providers are the passkey providers whose AAGUID is well known; others leave Passkey.Provider empty.
var providers = map[uuid.UUID]string{
	uuid.MustParse("fbfc3007-154e-4ecc-8c0b-6e020557d7bd"): "iCloud Keychain",
	uuid.MustParse("dd4ec289-e01d-41c9-bb89-70fa845d4bf2"): "iCloud Keychain",
	uuid.MustParse("ea9b8d66-4d01-1d21-3ce4-b6b48cb575d4"): "Google Password Manager",
	uuid.MustParse("adce0002-35bc-c60a-648b-0b25f1f05503"): "Chrome on Mac",
	uuid.MustParse("08987058-cadc-4b81-b6e1-30de50dcbe96"): "Windows Hello",
	uuid.MustParse("9ddd1817-af5a-4672-a2b9-3e3dd95000a9"): "Windows Hello",
	uuid.MustParse("6028b017-b1d4-4c02-b4b3-afcdafc96bb2"): "Windows Hello",
	uuid.MustParse("bada5566-a7aa-401f-bd96-45619a55120d"): "1Password",
	uuid.MustParse("d548826e-79b4-db40-a3d8-11116f7e8349"): "Bitwarden",
	uuid.MustParse("53414d53-554e-4700-0000-000000000000"): "Samsung Pass",
}

// providerName returns the provider of an authenticator AAGUID, or "".
func providerName(aaguid []byte) string {
	id, err := uuid.FromBytes(aaguid)
	if err != nil {
		return ""
	}
	return providers[id]
}
