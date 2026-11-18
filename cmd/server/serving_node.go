package main

import (
	"errors"

	"github.com/lkhmm520/portloom/internal/domain"
)

// loadServingNodeID reads the optional operator-controlled identity. It must not
// come from the request, the upstream, or the management hostname.
func loadServingNodeID(getenv envLookup) (string, error) {
	id := getenv("TM_SERVING_NODE_ID")
	if id != "" && !domain.ValidServingNodeID(id) {
		return "", errors.New("TM_SERVING_NODE_ID must be a single valid serving node ID")
	}
	return id, nil
}
