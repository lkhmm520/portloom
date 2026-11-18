package domain

import "strings"

// ServingNodeHeader is reserved for identity asserted by a trusted serving edge.
const ServingNodeHeader = "PortLoom-Serving-Node"

// ValidServingNodeID accepts a single, exact HTTP field value, not a list.
func ValidServingNodeID(id string) bool {
	if id == "" || strings.TrimSpace(id) != id || strings.Contains(id, ",") {
		return false
	}
	for i := 0; i < len(id); i++ {
		if id[i] < ' ' || id[i] == 0x7f {
			return false
		}
	}
	return true
}
