//go:build windows

package awg

import awgconf "github.com/amnezia-vpn/amneziawg-windows/v3/conf"

func validateOfficial(configuration string) error {
	_, err := awgconf.FromWgQuick(configuration, "Nexus")
	return err
}
