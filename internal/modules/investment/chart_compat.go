package investment

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"path"
	"strings"
)

//go:embed chart/tv-quotes-compat.json
var detailsQuotesPatchJSON []byte

var detailsQuotesPatch = func() struct{ Bundle, From, To string } {
	var patch struct{ Bundle, From, To string }
	if err := json.Unmarshal(detailsQuotesPatchJSON, &patch); err != nil {
		panic(err)
	}
	return patch
}()

// This upstream Details bundle drops night fields when copying quote values
// into its state. Keep the fix tied to that exact bundle; future versions pass
// through untouched. Cached upstream bodies remain unmodified.
func patchTVDetailsQuotes(assetPath string, header http.Header, body []byte) ([]byte, error) {
	if path.Base(assetPath) != detailsQuotesPatch.Bundle {
		return body, nil
	}
	decoded, err := decodeChartBody(header, body)
	if err != nil {
		return nil, err
	}
	from := []byte(detailsQuotesPatch.From)
	if bytes.Count(decoded, from) == 1 {
		decoded = bytes.Replace(decoded, from, []byte(detailsQuotesPatch.To), 1)
		body = decoded
		if strings.EqualFold(header.Get("Content-Encoding"), "gzip") {
			var compressed bytes.Buffer
			zip := gzip.NewWriter(&compressed)
			if _, err := zip.Write(decoded); err != nil {
				return nil, err
			}
			if err := zip.Close(); err != nil {
				return nil, err
			}
			body = compressed.Bytes()
		}
		header.Set("ETag", fmt.Sprintf(`"%x"`, sha256.Sum256(body)))
		header.Del("Content-MD5")
		header.Del("Digest")
	}
	return body, nil
}
