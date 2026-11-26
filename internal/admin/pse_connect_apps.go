package admin

import (
	"encoding/base64"
	"net/url"
	"strconv"

	qrcode "github.com/skip2/go-qrcode"
)

// pseStremioFiles ajoute au résultat le second lien Stremio d'un profil :
// même jeton, addon « fichiers seuls » (aucun catalogue), avec son QR code.
// Voir docs/pse/FORK.fr.md.
func pseStremioFiles(result map[string]string, u *url.URL, basePath, secret string) {
	f := *u
	f.Path = basePath + "/" + secret + "/fichiers/manifest.json"
	result["filesUrl"] = f.String()
	if png, err := qrcode.Encode(f.String(), qrcode.Medium, 256); err == nil {
		result["filesQr"] = "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)
	}
	pseStremioOptionQRs(result, u, basePath+"/"+secret, "qr")
	pseStremioOptionQRs(result, u, basePath+"/"+secret+"/fichiers", "filesQr")
}

// pseStremioOptionQRs ajoute le QR code de chaque lien avec options
// (…/o/<format>-<n>/manifest.json), clé « <prefixe>:<format>-<n> » : le secret
// n'étant montré qu'une fois, la console ne peut pas le redemander.
func pseStremioOptionQRs(result map[string]string, u *url.URL, linkPath, prefix string) {
	for _, format := range []string{"compact", "detaille"} {
		for n := 1; n <= 10; n++ {
			seg := format + "-" + strconv.Itoa(n)
			l := *u
			l.Path = linkPath + "/o/" + seg + "/manifest.json"
			if png, err := qrcode.Encode(l.String(), qrcode.Medium, 256); err == nil {
				result[prefix+":"+seg] = "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)
			}
		}
	}
}
