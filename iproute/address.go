package iproute

import (
	"encoding/json"

	"github.com/dropbox/godropbox/errors"
	"github.com/pritunl/pritunl-cloud/errortypes"
	"github.com/pritunl/pritunl-cloud/utils"
)

type Address struct {
	Family  string `json:"family"`
	Local   string `json:"local"`
	Prefix  int    `json:"prefixlen"`
	Scope   string `json:"scope"`
	Dynamic bool   `json:"dynamic"`
}

type AddressIface struct {
	Name      string     `json:"ifname"`
	State     string     `json:"operstate"`
	Addresses []*Address `json:"addr_info"`
}

func AddressGetIface(namespace, name string) (
	address, address6 *Address, err error) {

	ifaces := []*AddressIface{}

	var output string
	if namespace != "" {
		output, err = utils.ExecOutputLogged(
			[]string{
				"No such file or directory",
				"does not exist",
				"setting the network namespace",
			},
			"ip", "netns", "exec", namespace,
			"ip", "--json",
			"addr", "show",
			"dev", name,
		)
	} else {
		output, err = utils.ExecOutputLogged(
			[]string{
				"No such file or directory",
				"does not exist",
				"setting the network namespace",
			},
			"ip", "--json",
			"addr", "show",
			"dev", name,
		)
	}
	if err != nil {
		return
	}

	if output == "" {
		return
	}

	err = json.Unmarshal([]byte(output), &ifaces)
	if err != nil {
		err = &errortypes.ParseError{
			errors.Wrap(err, "iproute: Failed to parse interface address"),
		}
		return
	}

	dynamic6 := true
	for _, iface := range ifaces {
		if iface.Name == name && iface.Addresses != nil {
			for _, addr := range iface.Addresses {
				if addr.Scope == "global" {
					if address == nil && addr.Family == "inet" {
						address = addr
					} else if (address6 == nil || dynamic6) &&
						addr.Family == "inet6" {

						if !addr.Dynamic {
							dynamic6 = false
						}

						address6 = addr
					}
				}
			}
		}
	}

	return
}
