package app

import (
	"fmt"
	"net/url"
	"os/exec"
	"runtime"
	"time"
)

const (
	DisplayName = "AltHex"
	Website     = "https://altbins.pro/althex/"
	DocsURL     = Website + "docs/"
	Author      = "Ivan Poluianov"
	License     = "MIT"

	copyrightStartYear = 2026
)

// Version is set at build time from the git tag:
// -ldflags "-X github.com/ipoluianov/althex/app.Version=<tag>"
var Version = "dev"

// Copyright returns "Copyright © 2026-<current year> <author>"
func Copyright() string {
	years := fmt.Sprint(copyrightStartYear)
	if y := time.Now().Year(); y > copyrightStartYear {
		years = fmt.Sprintf("%d-%d", copyrightStartYear, y)
	}
	return "Copyright © " + years + " " + Author
}

// OpenSiteURL opens a page of the site; the UTM tags tell which place
// in the app the visit came from (campaign)
func OpenSiteURL(pageURL string, campaign string) error {
	q := url.Values{}
	q.Set("utm_source", "althex")
	q.Set("utm_medium", "app")
	q.Set("utm_campaign", campaign)
	q.Set("utm_content", Version)
	return OpenURL(pageURL + "?" + q.Encode())
}

// OpenURL opens the url in the default browser
func OpenURL(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}
