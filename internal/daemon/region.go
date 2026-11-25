package daemon

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// LandmarkRoles defines ARIA landmark roles used for region memoization.
var LandmarkRoles = map[string]bool{
	"navigation":    true,
	"main":          true,
	"banner":        true,
	"contentinfo":   true,
	"complementary": true,
	"region":        true,
	"form":          true,
	"search":        true,
}

// IsLandmarkRole returns true if role is a recognised ARIA landmark.
func IsLandmarkRole(role string) bool {
	return LandmarkRoles[strings.ToLower(strings.TrimSpace(role))]
}

// RegionSegment represents an identified landmark region in the accessibility tree.
type RegionSegment struct {
	RootIndex int
	Ref       string
	Role      string
	Name      string
	Nodes     []SnapNode
	Hash      string
}

// ComputeRegionHash computes a deterministic content hash for a slice of nodes in a region.
func ComputeRegionHash(nodes []SnapNode) string {
	var sb strings.Builder
	for _, n := range nodes {
		sb.WriteString(fmt.Sprintf("%d:%s:%s:%s:%s\n", n.Depth, n.Role, n.Name, n.Value, strings.Join(n.Props, ",")))
	}
	h := sha256.Sum256([]byte(sb.String()))
	return hex.EncodeToString(h[:8]) // 16 hex chars
}

// ExtractLandmarkRegions finds top-level landmark regions in a node slice.
func ExtractLandmarkRegions(nodes []SnapNode) []RegionSegment {
	var regions []RegionSegment
	skipUntil := -1

	for i, n := range nodes {
		if i < skipUntil {
			continue
		}
		if IsLandmarkRole(n.Role) {
			// Find extent of this region (all subsequent nodes with Depth > n.Depth)
			end := i + 1
			for end < len(nodes) && nodes[end].Depth > n.Depth {
				end++
			}
			regionNodes := nodes[i:end]
			hash := ComputeRegionHash(regionNodes)
			regions = append(regions, RegionSegment{
				RootIndex: i,
				Ref:       n.Ref,
				Role:      strings.ToLower(strings.TrimSpace(n.Role)),
				Name:      n.Name,
				Nodes:     regionNodes,
				Hash:      hash,
			})
			skipUntil = end
		}
	}
	return regions
}

// MemoizedRegion describes a region that was collapsed to a cache reference.
type MemoizedRegion struct {
	Ref  string `json:"ref"`
	Role string `json:"role"`
	Name string `json:"name,omitempty"`
	Hash string `json:"hash"`
}

// MemoizeRegions replaces unchanged landmark regions with placeholder references.
// It returns the transformed nodes, a map of memoized regions keyed by ref, and the updated region hashes.
func MemoizeRegions(nodes []SnapNode, prevHashes map[string]string) (outNodes []SnapNode, memoized map[string]MemoizedRegion, newHashes map[string]string) {
	newHashes = make(map[string]string)
	memoized = make(map[string]MemoizedRegion)

	regions := ExtractLandmarkRegions(nodes)
	if len(regions) == 0 {
		return nodes, memoized, newHashes
	}

	// Map start index to region
	regionByStart := make(map[int]RegionSegment, len(regions))
	for _, r := range regions {
		regionByStart[r.RootIndex] = r
		// Region key in session state is role (or role:name if name exists)
		regionKey := r.Role
		if r.Name != "" {
			regionKey += ":" + r.Name
		}
		newHashes[regionKey] = r.Hash
	}

	outNodes = make([]SnapNode, 0, len(nodes))
	i := 0
	for i < len(nodes) {
		if r, ok := regionByStart[i]; ok {
			regionKey := r.Role
			if r.Name != "" {
				regionKey += ":" + r.Name
			}
			// If previous hash exists and matches current hash, collapse region!
			if prevHash, exists := prevHashes[regionKey]; exists && prevHash == r.Hash {
				rootNode := r.Nodes[0]
				outNodes = append(outNodes, rootNode)
				memoized[rootNode.Ref] = MemoizedRegion{
					Ref:  rootNode.Ref,
					Role: r.Role,
					Name: r.Name,
					Hash: r.Hash,
				}
				i += len(r.Nodes)
				continue
			}
		}
		outNodes = append(outNodes, nodes[i])
		i++
	}

	return outNodes, memoized, newHashes
}
