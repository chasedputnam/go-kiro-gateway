// Package errors provides error classification, enhancement, and formatting
// for the Kiro Gateway. It translates low-level Go errors and upstream API
// errors into user-friendly messages with actionable troubleshooting steps.
package errors

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
)

// ---------------------------------------------------------------------------
// Error categories
// ---------------------------------------------------------------------------

// ErrorCategory classifies a network error into a specific failure type.
// Each category maps to distinct troubleshooting guidance.
type ErrorCategory string

const (
	CategoryDNSResolution      ErrorCategory = "dns_resolution"
	CategoryConnectionRefused  ErrorCategory = "connection_refused"
	CategoryConnectionReset    ErrorCategory = "connection_reset"
	CategoryNetworkUnreachable ErrorCategory = "network_unreachable"
	CategoryTimeoutConnect     ErrorCategory = "timeout_connect"
	CategoryTimeoutRead        ErrorCategory = "timeout_read"
	CategorySSLError           ErrorCategory = "ssl_error"
	CategoryProxyError         ErrorCategory = "proxy_error"
	CategoryUnknown            ErrorCategory = "unknown"
)

// ---------------------------------------------------------------------------
// NetworkErrorInfo
// ---------------------------------------------------------------------------

// NetworkErrorInfo holds structured information about a classified network
// error, including a user-friendly message and troubleshooting steps.
type NetworkErrorInfo struct {
	// Category is the error classification for programmatic use.
	Category ErrorCategory

	// UserMessage is a clear, non-technical message for end users.
	UserMessage string

	// TroubleshootingSteps lists actionable steps to resolve the issue.
	TroubleshootingSteps []string

	// TechnicalDetails contains the original error string for logging.
	TechnicalDetails string

	// IsRetryable indicates whether retrying the request might succeed.
	IsRetryable bool

	// SuggestedHTTPCode is the appropriate HTTP status code (502, 504, etc.).
	SuggestedHTTPCode int
}

// ---------------------------------------------------------------------------
// ClassifyNetworkError
// ---------------------------------------------------------------------------

// ClassifyNetworkError inspects a Go error and returns a NetworkErrorInfo
// with the appropriate category, user message, and troubleshooting steps.
//
// It unwraps the error chain to detect specific net, tls, and url errors,
// and falls back to string matching for cases where the error type is
// wrapped or opaque.
func ClassifyNetworkError(err error) *NetworkErrorInfo {
	if err == nil {
		return &NetworkErrorInfo{
			Category:             CategoryUnknown,
			UserMessage:          "An unexpected error occurred.",
			TroubleshootingSteps: []string{"Check the debug logs for details"},
			TechnicalDetails:     "",
			IsRetryable:          false,
			SuggestedHTTPCode:    500,
		}
	}

	technical := err.Error()

	// Check for DNS resolution errors (net.DNSError).
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return &NetworkErrorInfo{
			Category:    CategoryDNSResolution,
			UserMessage: "DNS resolution failed - cannot resolve the provider's domain name.",
			TroubleshootingSteps: []string{
				"Check your internet connection",
				"Try changing DNS servers to Google DNS (8.8.8.8, 8.8.4.4) or Cloudflare (1.1.1.1, 1.0.0.1)",
				"Temporarily disable VPN if you're using one",
				"Check if firewall/antivirus is blocking DNS requests",
				"Verify the domain name is correct and the service is operational",
			},
			TechnicalDetails:  technical,
			IsRetryable:       true,
			SuggestedHTTPCode: 502,
		}
	}

	// Check for TLS/SSL errors.
	var tlsRecordErr *tls.RecordHeaderError
	if errors.As(err, &tlsRecordErr) {
		return classifySSLError(technical)
	}
	// Also check by string for wrapped TLS errors that don't expose a
	// concrete type (e.g. x509 certificate errors).
	errLower := strings.ToLower(technical)
	if strings.Contains(errLower, "tls") ||
		strings.Contains(errLower, "ssl") ||
		strings.Contains(errLower, "certificate") ||
		strings.Contains(errLower, "x509") {
		return classifySSLError(technical)
	}

	// Check for proxy errors (url.Error wrapping proxy issues).
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		if strings.Contains(errLower, "proxy") ||
			strings.Contains(errLower, "proxyconnect") {
			return &NetworkErrorInfo{
				Category:    CategoryProxyError,
				UserMessage: "Proxy connection failed - cannot connect through the configured proxy.",
				TroubleshootingSteps: []string{
					"Check proxy configuration (VPN_PROXY_URL environment variable)",
					"Verify proxy server is accessible",
					"Try disabling proxy temporarily",
					"Check proxy authentication credentials if required",
				},
				TechnicalDetails:  technical,
				IsRetryable:       true,
				SuggestedHTTPCode: 502,
			}
		}
	}

	// Check for net.OpError which wraps most low-level network failures.
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return classifyOpError(opErr, technical)
	}

	// Timeout detection via the net.Error interface (covers all timeout types).
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return classifyTimeoutFromString(technical)
	}

	// Fall back to string-based classification for wrapped errors.
	return classifyByString(technical)
}

// ---------------------------------------------------------------------------
// Sub-classifiers
// ---------------------------------------------------------------------------

// classifyOpError inspects a *net.OpError to determine the specific failure.
func classifyOpError(opErr *net.OpError, technical string) *NetworkErrorInfo {
	errStr := opErr.Error()

	// Connection refused.
	if strings.Contains(errStr, "connection refused") ||
		strings.Contains(errStr, "ECONNREFUSED") {
		return &NetworkErrorInfo{
			Category:    CategoryConnectionRefused,
			UserMessage: "Connection refused - the server is not accepting connections.",
			TroubleshootingSteps: []string{
				"The service may be temporarily down",
				"Check if the service is running and accessible",
				"Verify firewall is not blocking the connection",
				"Try again in a few moments",
			},
			TechnicalDetails:  technical,
			IsRetryable:       true,
			SuggestedHTTPCode: 502,
		}
	}

	// Connection reset.
	if strings.Contains(errStr, "connection reset") ||
		strings.Contains(errStr, "ECONNRESET") {
		return &NetworkErrorInfo{
			Category:    CategoryConnectionReset,
			UserMessage: "Connection reset - the server closed the connection unexpectedly.",
			TroubleshootingSteps: []string{
				"This is usually a temporary server issue",
				"Try again in a few moments",
				"Check if VPN/proxy is interfering with the connection",
				"Verify network stability",
			},
			TechnicalDetails:  technical,
			IsRetryable:       true,
			SuggestedHTTPCode: 502,
		}
	}

	// Network unreachable.
	if strings.Contains(errStr, "network is unreachable") ||
		strings.Contains(errStr, "no route to host") ||
		strings.Contains(errStr, "ENETUNREACH") {
		return &NetworkErrorInfo{
			Category:    CategoryNetworkUnreachable,
			UserMessage: "Network unreachable - cannot reach the server's network.",
			TroubleshootingSteps: []string{
				"Check your internet connection",
				"Verify network adapter is enabled and working",
				"Check routing table if using VPN",
				"Try disabling VPN temporarily",
				"Restart network adapter or router",
			},
			TechnicalDetails:  technical,
			IsRetryable:       true,
			SuggestedHTTPCode: 502,
		}
	}

	// Timeout on the operation.
	if opErr.Timeout() {
		return classifyTimeoutFromString(technical)
	}

	// Generic OpError fallback.
	return &NetworkErrorInfo{
		Category:    CategoryUnknown,
		UserMessage: "Connection failed - unable to establish connection to the server.",
		TroubleshootingSteps: []string{
			"Check your internet connection",
			"Verify firewall/antivirus settings",
			"Try disabling VPN temporarily",
			"Check if the service is accessible from other devices",
		},
		TechnicalDetails:  technical,
		IsRetryable:       true,
		SuggestedHTTPCode: 502,
	}
}

// classifyTimeoutFromString determines whether a timeout is a connect timeout
// or a read timeout based on the error message content.
func classifyTimeoutFromString(technical string) *NetworkErrorInfo {
	lower := strings.ToLower(technical)

	if strings.Contains(lower, "dial") || strings.Contains(lower, "connect") {
		return &NetworkErrorInfo{
			Category:    CategoryTimeoutConnect,
			UserMessage: "Connection timeout - server did not respond to connection attempt.",
			TroubleshootingSteps: []string{
				"Check your internet connection speed",
				"The server may be overloaded or slow to respond",
				"Try again in a few moments",
				"Check if firewall is delaying connections",
			},
			TechnicalDetails:  technical,
			IsRetryable:       true,
			SuggestedHTTPCode: 504,
		}
	}

	// Default to read timeout.
	return &NetworkErrorInfo{
		Category:    CategoryTimeoutRead,
		UserMessage: "Read timeout - server stopped responding during data transfer.",
		TroubleshootingSteps: []string{
			"The server may be processing a complex request",
			"Check your internet connection stability",
			"Try again with a simpler request",
			"The service may be experiencing high load",
		},
		TechnicalDetails:  technical,
		IsRetryable:       true,
		SuggestedHTTPCode: 504,
	}
}

// classifySSLError returns a NetworkErrorInfo for SSL/TLS failures.
func classifySSLError(technical string) *NetworkErrorInfo {
	return &NetworkErrorInfo{
		Category:    CategorySSLError,
		UserMessage: "SSL/TLS error - secure connection could not be established.",
		TroubleshootingSteps: []string{
			"Check system date and time (incorrect time causes SSL errors)",
			"Update SSL certificates on your system",
			"Check if antivirus/firewall is intercepting HTTPS traffic",
			"Verify the server's SSL certificate is valid",
		},
		TechnicalDetails:  technical,
		IsRetryable:       false,
		SuggestedHTTPCode: 502,
	}
}

// classifyByString is the last-resort classifier that inspects the error
// string when no typed error was matched.
func classifyByString(technical string) *NetworkErrorInfo {
	lower := strings.ToLower(technical)

	if strings.Contains(lower, "connection refused") {
		return &NetworkErrorInfo{
			Category:    CategoryConnectionRefused,
			UserMessage: "Connection refused - the server is not accepting connections.",
			TroubleshootingSteps: []string{
				"The service may be temporarily down",
				"Check if the service is running and accessible",
				"Verify firewall is not blocking the connection",
				"Try again in a few moments",
			},
			TechnicalDetails:  technical,
			IsRetryable:       true,
			SuggestedHTTPCode: 502,
		}
	}

	if strings.Contains(lower, "connection reset") {
		return &NetworkErrorInfo{
			Category:    CategoryConnectionReset,
			UserMessage: "Connection reset - the server closed the connection unexpectedly.",
			TroubleshootingSteps: []string{
				"This is usually a temporary server issue",
				"Try again in a few moments",
				"Check if VPN/proxy is interfering with the connection",
				"Verify network stability",
			},
			TechnicalDetails:  technical,
			IsRetryable:       true,
			SuggestedHTTPCode: 502,
		}
	}

	if strings.Contains(lower, "network is unreachable") || strings.Contains(lower, "no route to host") {
		return &NetworkErrorInfo{
			Category:    CategoryNetworkUnreachable,
			UserMessage: "Network unreachable - cannot reach the server's network.",
			TroubleshootingSteps: []string{
				"Check your internet connection",
				"Verify network adapter is enabled and working",
				"Check routing table if using VPN",
				"Try disabling VPN temporarily",
				"Restart network adapter or router",
			},
			TechnicalDetails:  technical,
			IsRetryable:       true,
			SuggestedHTTPCode: 502,
		}
	}

	if strings.Contains(lower, "proxy") {
		return &NetworkErrorInfo{
			Category:    CategoryProxyError,
			UserMessage: "Proxy connection failed - cannot connect through the configured proxy.",
			TroubleshootingSteps: []string{
				"Check proxy configuration (VPN_PROXY_URL environment variable)",
				"Verify proxy server is accessible",
				"Try disabling proxy temporarily",
				"Check proxy authentication credentials if required",
			},
			TechnicalDetails:  technical,
			IsRetryable:       true,
			SuggestedHTTPCode: 502,
		}
	}

	// Catch-all unknown error.
	return &NetworkErrorInfo{
		Category:    CategoryUnknown,
		UserMessage: "Network request failed due to an unexpected error.",
		TroubleshootingSteps: []string{
			"Check your internet connection",
			"Verify firewall/antivirus settings",
			"Try again in a few moments",
			"Check the debug logs for more details",
		},
		TechnicalDetails:  technical,
		IsRetryable:       true,
		SuggestedHTTPCode: 502,
	}
}

// ---------------------------------------------------------------------------
// Formatting helpers
// ---------------------------------------------------------------------------

// FormatUserMessage returns the user message with numbered troubleshooting
// steps appended. This is the string typically included in API error responses.
func (info *NetworkErrorInfo) FormatUserMessage() string {
	if len(info.TroubleshootingSteps) == 0 {
		return info.UserMessage
	}

	var b strings.Builder
	b.WriteString(info.UserMessage)
	b.WriteString("\n\nTroubleshooting steps:\n")
	for i, step := range info.TroubleshootingSteps {
		fmt.Fprintf(&b, "%d. %s\n", i+1, step)
	}
	return strings.TrimRight(b.String(), "\n")
}
