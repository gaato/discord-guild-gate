package main

import (
	"net/http/httputil"
	"net/url"
	"time"
)

func newProxy(upstream *url.URL) *httputil.ReverseProxy {
	p := httputil.NewSingleHostReverseProxy(upstream)
	p.FlushInterval = 100 * time.Millisecond
	return p
}
