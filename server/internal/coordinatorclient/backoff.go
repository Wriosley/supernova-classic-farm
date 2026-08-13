package coordinatorclient

// Backoff policy is implemented by Client.watchLoop. It is deliberately one
// reconnect loop per client; business route lookups never create retries.
