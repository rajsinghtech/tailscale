// Copyright (c) Tailscale Inc & AUTHORS
// SPDX-License-Identifier: BSD-3-Clause

// Package nmcfg converts a controlclient.NetMap into a wgcfg config.
package nmcfg

import (
	"bufio"
	"cmp"
	"fmt"
	"net/netip"
	"strings"

	"tailscale.com/tailcfg"
	"tailscale.com/types/key"
	"tailscale.com/types/logger"
	"tailscale.com/types/logid"
	"tailscale.com/types/netmap"
	"tailscale.com/wgengine/wgcfg"
)

func nodeDebugName(n tailcfg.NodeView) string {
	name, _, _ := strings.Cut(cmp.Or(n.Name(), n.Hostinfo().Hostname()), ".")
	return name
}

// cidrIsSubnet reports whether cidr is a non-default-route subnet
// exported by node that is not one of its own self addresses.
func cidrIsSubnet(node tailcfg.NodeView, cidr netip.Prefix) bool {
	if cidr.Bits() == 0 {
		return false
	}
	if !cidr.IsSingleIP() {
		return true
	}
	for _, selfCIDR := range node.Addresses().All() {
		if cidr == selfCIDR {
			return false
		}
	}
	return true
}

// haSubnetRouterAllowedIPs returns additional AllowedIPs for HA subnet router
// site-to-site scenarios. When control pairs HA subnet routers 1:1, traffic may
// arrive from an "unpaired" peer whose subnet isn't in AllowedIPs, causing
// WireGuard to drop packets. This expands AllowedIPs to include subnets from
// all peers sharing the same tags (HA group members).
func haSubnetRouterAllowedIPs(selfNode tailcfg.NodeView, peer tailcfg.NodeView, allPeers []tailcfg.NodeView) []netip.Prefix {
	// Only applies if self is a subnet router (has subnet routes in AllowedIPs)
	if !selfNode.Valid() {
		return nil
	}
	selfIsSubnetRouter := false
	for _, aip := range selfNode.AllowedIPs().All() {
		if cidrIsSubnet(selfNode, aip) {
			selfIsSubnetRouter = true
			break
		}
	}
	if !selfIsSubnetRouter {
		return nil
	}

	// Peer must have tags for HA group matching
	peerTags := peer.Tags()
	if peerTags.Len() == 0 {
		return nil
	}

	var additionalIPs []netip.Prefix
	seen := make(map[netip.Prefix]bool)

	// Mark peer's existing AllowedIPs as seen
	for _, aip := range peer.AllowedIPs().All() {
		seen[aip] = true
	}

	// Find peers in the same HA group (share at least one tag)
	for _, otherPeer := range allPeers {
		if otherPeer.ID() == peer.ID() {
			continue
		}

		// otherPeer must have PrimaryRoutes to contribute routes
		// (only "paired" peers have PrimaryRoutes set by control)
		if otherPeer.PrimaryRoutes().Len() == 0 {
			continue
		}

		// Check if otherPeer shares a tag with peer (same HA group)
		otherTags := otherPeer.Tags()
		hasSharedTag := false
		for _, pt := range peerTags.All() {
			for _, ot := range otherTags.All() {
				if pt == ot {
					hasSharedTag = true
					break
				}
			}
			if hasSharedTag {
				break
			}
		}
		if !hasSharedTag {
			continue
		}

		// Add routes from this HA group member
		for _, route := range otherPeer.PrimaryRoutes().All() {
			if seen[route] {
				continue
			}
			additionalIPs = append(additionalIPs, route)
			seen[route] = true
		}
	}

	return additionalIPs
}

// WGCfg returns the NetworkMaps's WireGuard configuration.
func WGCfg(pk key.NodePrivate, nm *netmap.NetworkMap, logf logger.Logf, flags netmap.WGConfigFlags, exitNode tailcfg.StableNodeID) (*wgcfg.Config, error) {
	cfg := &wgcfg.Config{
		PrivateKey: pk,
		Addresses:  nm.GetAddresses().AsSlice(),
		Peers:      make([]wgcfg.Peer, 0, len(nm.Peers)),
	}

	// Setup log IDs for data plane audit logging.
	if nm.SelfNode.Valid() {
		canNetworkLog := nm.SelfNode.HasCap(tailcfg.CapabilityDataPlaneAuditLogs)
		logExitFlowEnabled := nm.SelfNode.HasCap(tailcfg.NodeAttrLogExitFlows)
		if canNetworkLog && nm.SelfNode.DataPlaneAuditLogID() != "" && nm.DomainAuditLogID != "" {
			nodeID, errNode := logid.ParsePrivateID(nm.SelfNode.DataPlaneAuditLogID())
			if errNode != nil {
				logf("[v1] wgcfg: unable to parse node audit log ID: %v", errNode)
			}
			domainID, errDomain := logid.ParsePrivateID(nm.DomainAuditLogID)
			if errDomain != nil {
				logf("[v1] wgcfg: unable to parse domain audit log ID: %v", errDomain)
			}
			if errNode == nil && errDomain == nil {
				cfg.NetworkLogging.NodeID = nodeID
				cfg.NetworkLogging.DomainID = domainID
				cfg.NetworkLogging.LogExitFlowEnabled = logExitFlowEnabled
			}
		}
	}

	var skippedExitNode, skippedSubnetRouter, skippedExpired []tailcfg.NodeView

	for _, peer := range nm.Peers {
		if peer.DiscoKey().IsZero() && peer.HomeDERP() == 0 && !peer.IsWireGuardOnly() {
			// Peer predates both DERP and active discovery, we cannot
			// communicate with it.
			logf("[v1] wgcfg: skipped peer %s, doesn't offer DERP or disco", peer.Key().ShortString())
			continue
		}
		// Skip expired peers; we'll end up failing to connect to them
		// anyway, since control intentionally breaks node keys for
		// expired peers so that we can't discover endpoints via DERP.
		if peer.Expired() {
			skippedExpired = append(skippedExpired, peer)
			continue
		}

		cfg.Peers = append(cfg.Peers, wgcfg.Peer{
			PublicKey: peer.Key(),
			DiscoKey:  peer.DiscoKey(),
		})
		cpeer := &cfg.Peers[len(cfg.Peers)-1]

		didExitNodeLog := false
		cpeer.V4MasqAddr = peer.SelfNodeV4MasqAddrForThisPeer().Clone()
		cpeer.V6MasqAddr = peer.SelfNodeV6MasqAddrForThisPeer().Clone()
		cpeer.IsJailed = peer.IsJailed()
		for _, allowedIP := range peer.AllowedIPs().All() {
			if allowedIP.Bits() == 0 && peer.StableID() != exitNode {
				if didExitNodeLog {
					// Don't log about both the IPv4 /0 and IPv6 /0.
					continue
				}
				didExitNodeLog = true
				skippedExitNode = append(skippedExitNode, peer)
				continue
			} else if cidrIsSubnet(peer, allowedIP) {
				if (flags & netmap.AllowSubnetRoutes) == 0 {
					skippedSubnetRouter = append(skippedSubnetRouter, peer)
					continue
				}
			}
			cpeer.AllowedIPs = append(cpeer.AllowedIPs, allowedIP)
		}

		// For HA subnet router site-to-site scenarios, expand AllowedIPs
		// to include subnets from HA group members. This fixes the issue
		// where traffic arriving at one SR in an HA group gets dropped
		// because it came from a different SR than the one it's "paired" with.
		if (flags & netmap.AllowSubnetRoutes) != 0 {
			haIPs := haSubnetRouterAllowedIPs(nm.SelfNode, peer, nm.Peers)
			if len(haIPs) > 0 {
				logf("[v1] wgcfg: adding %d HA subnet router AllowedIPs for peer %s: %v",
					len(haIPs), nodeDebugName(peer), haIPs)
				cpeer.AllowedIPs = append(cpeer.AllowedIPs, haIPs...)
			}
		}
	}

	logList := func(title string, nodes []tailcfg.NodeView) {
		if len(nodes) == 0 {
			return
		}
		logf("[v1] wgcfg: %s from %d nodes: %s", title, len(nodes), logger.ArgWriter(func(bw *bufio.Writer) {
			const max = 5
			for i, n := range nodes {
				if i == max {
					fmt.Fprintf(bw, "... +%d", len(nodes)-max)
					return
				}
				if i > 0 {
					bw.WriteString(", ")
				}
				fmt.Fprintf(bw, "%s (%s)", nodeDebugName(n), n.StableID())
			}
		}))
	}
	logList("skipped unselected exit nodes", skippedExitNode)
	logList("did not accept subnet routes", skippedSubnetRouter)
	logList("skipped expired peers", skippedExpired)

	return cfg, nil
}
