package cmd

import "testing"

func TestSanitizeAccessLogPattern(t *testing.T) {
	cases := map[string]string{
		"${protocol} ${host} ${method} ${path} ${status} [${latency}] [${reqHeaders}] [${locals:cleanBody}]":     "${protocol} ${host} ${method} ${path} ${status} [${latency}] [${ua}] [${locals:cleanBody}]",
		"${body} ${resBody} ${url} ${queryParams} ${query:token} ${form:password} ${cookie:session}":             "      ",
		"${reqHeader:Authorization} ${reqHeader:User-Agent} ${respHeader:Set-Cookie} ${respHeader:content-type}": " ${reqHeader:User-Agent}  ${respHeader:content-type}",
		"${red}${status}${reset} ${unknownTag}": "${red}${status}${reset} ",
	}
	for in, want := range cases {
		if got := sanitizeAccessLogPattern(in); got != want {
			t.Errorf("sanitizeAccessLogPattern(%q) = %q, want %q", in, got, want)
		}
	}
}
