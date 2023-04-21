package internal

import "net/url"

type HtmlLink struct {
	Type string
	Href *url.URL
}

type Manifest struct {
	Icons []ManifestIcon `json:"icons"`
}

type ManifestIcon struct {
	Src   string `json:"src"`
	Sizes string `json:"sizes"`
	Type  string `json:"type"`
}
