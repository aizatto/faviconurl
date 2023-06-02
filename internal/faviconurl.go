package internal

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"strings"

	"golang.org/x/net/html"
)

var IconAttributeValues = []string{
	"icon",
	"apple-touch-icon",
	"apple-touch-icon-precomposed",
}

func ParseArgs(urls []string) error {
	for _, arg := range urls {
		url, err := parseArg(arg)
		if err != nil {
			fmt.Fprintf(os.Stdout, "%s\n", arg)
			fmt.Fprintf(os.Stderr, "Error: %v", err)
			continue
		}

		err = fetchAll(url)
		fmt.Fprintf(os.Stdout, "\n")
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v", err)
		}

		fmt.Fprintln(os.Stdout, "")
	}

	return nil
}

func parseArg(arg string) (*url.URL, error) {
	// Parse the argument as a URL
	u, err := url.Parse(arg)
	if err != nil {
		return nil, errors.New("invalid url")
	}

	// Check if the URL scheme is HTTP or HTTPS
	if u.Scheme != "http" && u.Scheme != "https" {
		if u.Scheme != "" {
			return nil, errors.New("URL scheme must be HTTP or HTTPS")
		}

		u.Scheme = "https"
	}

	// handles args like "google.com", where there is no host no scheme
	if u.Host == "" && len(u.Path) != 0 {
		u.Host = u.Path
		u.Path = ""
	}

	return u, nil
}

func fetchAll(originalUrl *url.URL) error {
	favicons := []string{}
	redirectUrl, _ := url.Parse(originalUrl.String())

	linksFromUrl, err := fetchLinksFromUrl(redirectUrl)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to fetch from url: %v", err)
	} else {
		for _, link := range linksFromUrl {
			if link.Type != "canonical" {
				continue
			}

			if link.Href.String() != redirectUrl.String() {
				resolveUrl(redirectUrl, link.Href)
				redirectUrl.Host = link.Href.Host
			}
		}

		for _, link := range linksFromUrl {
			faviconsFromLinks := parseFavIconsFromLinks(redirectUrl, link)
			favicons = append(favicons, faviconsFromLinks...)
		}
	}

	favicon, err := fetchFaviconFromDomainRoot(redirectUrl)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to fetch from domain root: %v", err)
	} else {
		favicons = append(favicons, favicon)
	}

	if originalUrl.String() == redirectUrl.String() {
		fmt.Fprintf(os.Stdout, "%s\n", originalUrl.String())
	} else {
		fmt.Fprintf(os.Stdout, "%s -> %s\n", originalUrl.String(), redirectUrl.String())
	}

	for i, favicon := range favicons {
		fmt.Fprintf(os.Stdout, "%d. %s\n", i+1, favicon)
	}

	return nil
}

func fetchUrl(url string) (string, *http.Response, error) {
	client := newHttpClient()
	for {
		resp, err := client.Get(url)
		if err != nil {
			return url, nil, err
		}

		statusCode := resp.StatusCode
		// 300s
		if statusCode >= http.StatusMultipleChoices && statusCode < http.StatusBadRequest {
			resp.Body.Close()
			location := resp.Header.Get("Location")
			if location == "" {
				return url, nil, fmt.Errorf("location is empty for http status: %s %s (%d)", location, resp.Status, resp.StatusCode)
			}

			url = location
			continue
		}

		// non 200s
		if resp.StatusCode < http.StatusOK || resp.StatusCode > http.StatusMultipleChoices {
			resp.Body.Close()
			return url, nil, fmt.Errorf("invalid http status: %s %s (%d)", url, resp.Status, resp.StatusCode)
		}

		return url, resp, nil
	}
}

func fetchFaviconFromDomainRoot(url *url.URL) (string, error) {
	faviconUrl := fmt.Sprintf("%s://%s/favicon.ico", url.Scheme, url.Host)

	_, resp, err := fetchUrl(faviconUrl)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	return faviconUrl, nil
}

func fetchLinksFromUrl(url *url.URL) ([]*HtmlLink, error) {
	newUrl, resp, err := fetchUrl(url.String())
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if url.String() != newUrl {
		newUrlp, err := url.Parse(newUrl)
		if err != nil {
			return nil, fmt.Errorf("fail to parse new url: %s %v", newUrl, err)
		}
		*url = *newUrlp
	}

	contentType := strings.Split(resp.Header.Get("content-type"), ";")
	if contentType[0] != "text/html" {
		return nil, fmt.Errorf("unexpected content-type: %s", contentType[0])
	}

	// Read the favicon data from the response body
	htmlContent, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("error reading page: %v", err)
	}

	// Parse the HTML data into a node tree
	doc, err := html.Parse(strings.NewReader(string(htmlContent)))
	if err != nil {
		return nil, fmt.Errorf("failed to parse: %v", err)
	}

	return getLinks(url, doc), nil
}

// Parses a page to find all useful <link /> tags to be used later.
//
// This function is broken down into two parts
// 1. Recursive algorithm to find all the <link /> tags
// 2. Map found <link /> tags to an internal datatype
func getLinks(u *url.URL, node *html.Node) []*HtmlLink {
	if !(node.Type == html.ElementNode && (node.Data == "link" || node.Data == "meta")) {
		var icons []*HtmlLink
		for c := node.FirstChild; c != nil; c = c.NextSibling {
			newIcons := getLinks(u, c)
			if len(newIcons) != 0 {
				icons = append(icons, newIcons...)
			}
		}

		return icons
	}

	var link *HtmlLink
	switch node.Data {
	case "link":
		link = newHtmlLinkFromLinkNode(node)
	case "meta":
		link = newHtmlLinkFromMetaNode(node)
	default:
		fmt.Fprintf(os.Stderr, "Unsupported node element tag: %s", node.Data)
	}

	if link == nil {
		return nil
	}

	return []*HtmlLink{link}
}

func newHtmlLinkFromLinkNode(node *html.Node) *HtmlLink {
	linkRelType := ""

	// we do this to handle a race condition, what if `href` is discovered first
	var hrefUrl *url.URL

	// need to collect 2 attributes, rel and the href
	for _, attr := range node.Attr {
		value := attr.Val
		switch attr.Key {
		case "href":
			u, err := url.Parse(value)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error parsing link href %s %v", value, err)
				return nil
			}
			hrefUrl = u

		case "rel":
			switch value {
			case "canonical":
				linkRelType = "canonical"
			case "icon":
				linkRelType = "icon"
			case "manifest":
				linkRelType = "manifest"
			default:
				values := strings.Split(value, " ")

				// check that the type can be an icon
				for _, value := range values {
					for _, expected := range IconAttributeValues {
						if value != expected {
							continue
						}

						linkRelType = "icon"
						break
					}
				}
			}
		}
	}

	if linkRelType == "" || hrefUrl == nil {
		return nil
	}

	return &HtmlLink{Type: linkRelType, Href: hrefUrl}
}

func newHtmlLinkFromMetaNode(node *html.Node) *HtmlLink {
	// we do this to handle a race condition, what if `href` is discovered first
	var hrefUrl *url.URL
	imageTag := false

	// need to collect 2 attributes, rel and the href
	for _, attr := range node.Attr {
		value := attr.Val
		switch attr.Key {
		case "content":
			u, err := url.Parse(attr.Val)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error parsing link href %s %v", value, err)
				return nil
			}
			hrefUrl = u

		case "name":
			if !(value == "og:image" || value == "twitter:image") {
				return nil
			}

			imageTag = true
		}
	}

	if !imageTag || hrefUrl == nil {
		return nil
	}

	return &HtmlLink{Type: "icon", Href: hrefUrl}
}

func parseFavIconsFromLinks(u *url.URL, link *HtmlLink) []string {
	resolveUrl(u, link.Href)

	switch link.Type {
	case "icon":
		return []string{link.Href.String()}
	case "manifest":
		favicons, err := fetchFaviconFromManifest(link.Href)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%v", err)
			return []string{}
		}
		return favicons
	default:
		// should not be possible
		// TODO better error handling
		return []string{}
	}
}

func fetchFaviconFromManifest(u *url.URL) ([]string, error) {
	_, resp, err := fetchUrl(u.String())
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	contentType := strings.Split(resp.Header.Get("content-type"), ";")
	if contentType[0] != "application/json" {
		return nil, fmt.Errorf("unexpected content-type: %s", contentType[0])
	}

	// Read the favicon data from the response body
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("error reading page: %v", err)
	}

	var manifest Manifest
	err = json.Unmarshal(body, &manifest)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal manifest: %v", err)
	}

	favicons := []string{}
	for _, icon := range manifest.Icons {
		src := icon.Src
		srcUrl, err := url.Parse(src)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to parse manifest icon url: %s %v", src, err)
			continue
		}

		resolveUrl(u, srcUrl)
		favicons = append(favicons, srcUrl.String())
	}

	return favicons, nil
}

func resolveUrl(src, new *url.URL) {
	if !strings.HasPrefix(new.Path, "/") {
		new.Path = src.Path + "/" + new.Path
	}

	if new.Scheme == "" {
		new.Scheme = src.Scheme
	}

	if new.Host == "" {
		new.Host = src.Host
	}
}
