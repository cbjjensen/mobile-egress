package service

import "net/netip"

// orderedCandidates preserves resolver preference for the first family, then
// alternates families so a cellular family failure can reach the other quickly.
// Addresses have already passed the public-address policy before this is called.
func orderedCandidates(addresses []netip.Addr) []string {
	var families [2][]netip.Addr
	seen := make(map[netip.Addr]struct{}, len(addresses))
	firstFamily := 0
	for i, address := range addresses {
		address = address.Unmap()
		family := 0
		if address.Is6() {
			family = 1
		}
		if i == 0 {
			firstFamily = family
		}
		if _, ok := seen[address]; ok {
			continue
		}
		seen[address] = struct{}{}
		families[family] = append(families[family], address)
	}
	result := make([]string, 0, min(8, len(seen)))
	family := firstFamily
	for len(result) < 8 && len(families[0])+len(families[1]) > 0 {
		if len(families[family]) == 0 {
			family = 1 - family
		}
		result = append(result, families[family][0].String())
		families[family] = families[family][1:]
		family = 1 - family
	}
	return result
}
