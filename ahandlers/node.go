package ahandlers

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/dropbox/godropbox/container/set"
	"github.com/gin-gonic/gin"
	"github.com/pritunl/mongo-go-driver/bson"
	"github.com/pritunl/mongo-go-driver/bson/primitive"
	"github.com/pritunl/pritunl-cloud/database"
	"github.com/pritunl/pritunl-cloud/demo"
	"github.com/pritunl/pritunl-cloud/errortypes"
	"github.com/pritunl/pritunl-cloud/event"
	"github.com/pritunl/pritunl-cloud/node"
	"github.com/pritunl/pritunl-cloud/utils"
)

type nodeData struct {
	Id                   primitive.ObjectID      `json:"id"`
	Zone                 primitive.ObjectID      `json:"zone"`
	Name                 string                  `json:"name"`
	Comment              string                  `json:"comment"`
	Types                []string                `json:"types"`
	Port                 int                     `json:"port"`
	NoRedirectServer     bool                    `json:"no_redirect_server"`
	Protocol             string                  `json:"protocol"`
	Hypervisor           string                  `json:"hypervisor"`
	Vga                  string                  `json:"vga"`
	Certificates         []primitive.ObjectID    `json:"certificates"`
	AdminDomain          string                  `json:"admin_domain"`
	UserDomain           string                  `json:"user_domain"`
	Services             []primitive.ObjectID    `json:"services"`
	ExternalInterfaces   []string                `json:"external_interfaces"`
	ExternalInterfaces6  []string                `json:"external_interfaces6"`
	InternalInterfaces   []string                `json:"internal_interfaces"`
	NetworkMode          string                  `json:"network_mode"`
	NetworkMode6         string                  `json:"network_mode6"`
	Blocks               []*node.BlockAttachment `json:"blocks"`
	Blocks6              []*node.BlockAttachment `json:"blocks6"`
	HostBlock            primitive.ObjectID      `json:"host_block"`
	HostNat              bool                    `json:"host_nat"`
	HostNatExcludes      []string                `json:"host_nat_excludes"`
	JumboFrames          bool                    `json:"jumbo_frames"`
	UsbPassthrough       bool                    `json:"usb_passthrough"`
	ForwardedForHeader   string                  `json:"forwarded_for_header"`
	ForwardedProtoHeader string                  `json:"forwarded_proto_header"`
	Firewall             bool                    `json:"firewall"`
	NetworkRoles         []string                `json:"network_roles"`
	OracleUser           string                  `json:"oracle_user"`
	OracleHostRoute      bool                    `json:"oracle_host_route"`
}

type nodesData struct {
	Nodes []*node.Node `json:"nodes"`
	Count int64        `json:"count"`
}

func nodePut(c *gin.Context) {
	if demo.Blocked(c) {
		return
	}

	db := c.MustGet("db").(*database.Database)
	data := &nodeData{}

	nodeId, ok := utils.ParseObjectId(c.Param("node_id"))
	if !ok {
		utils.AbortWithStatus(c, 400)
		return
	}

	err := c.Bind(data)
	if err != nil {
		utils.AbortWithError(c, 500, err)
		return
	}

	nde, err := node.Get(db, nodeId)
	if err != nil {
		utils.AbortWithError(c, 500, err)
		return
	}

	nde.Name = data.Name
	nde.Comment = data.Comment
	nde.Types = data.Types
	nde.Port = data.Port
	nde.NoRedirectServer = data.NoRedirectServer
	nde.Protocol = data.Protocol
	nde.Hypervisor = data.Hypervisor
	nde.Vga = data.Vga
	nde.Certificates = data.Certificates
	nde.AdminDomain = data.AdminDomain
	nde.UserDomain = data.UserDomain
	nde.ExternalInterfaces = data.ExternalInterfaces
	nde.ExternalInterfaces6 = data.ExternalInterfaces6
	nde.InternalInterfaces = data.InternalInterfaces
	nde.NetworkMode = data.NetworkMode
	nde.NetworkMode6 = data.NetworkMode6
	nde.Blocks = data.Blocks
	nde.Blocks6 = data.Blocks6
	nde.HostBlock = data.HostBlock
	nde.HostNat = data.HostNat
	nde.HostNatExcludes = data.HostNatExcludes
	nde.JumboFrames = data.JumboFrames
	nde.UsbPassthrough = data.UsbPassthrough
	nde.ForwardedForHeader = data.ForwardedForHeader
	nde.ForwardedProtoHeader = data.ForwardedProtoHeader
	nde.Firewall = data.Firewall
	nde.NetworkRoles = data.NetworkRoles
	nde.OracleUser = data.OracleUser
	nde.OracleHostRoute = data.OracleHostRoute

	fields := set.NewSet(
		"name",
		"comment",
		"zone",
		"types",
		"port",
		"no_redirect_server",
		"protocol",
		"hypervisor",
		"vga",
		"certificates",
		"admin_domain",
		"user_domain",
		"external_interfaces",
		"external_interfaces6",
		"internal_interfaces",
		"network_mode",
		"network_mode6",
		"blocks",
		"blocks6",
		"host_block",
		"host_nat",
		"host_nat_excludes",
		"jumbo_frames",
		"usb_passthrough",
		"forwarded_for_header",
		"forwarded_proto_header",
		"firewall",
		"network_roles",
		"oracle_user",
		"oracle_host_route",
	)

	if !data.Zone.IsZero() && data.Zone != nde.Zone {
		if !nde.Zone.IsZero() {
			errData := &errortypes.ErrorData{
				Error:   "zone_modified",
				Message: "Cannot modify zone once set",
			}
			c.JSON(400, errData)
			return
		}
		nde.Zone = data.Zone
	}

	errData, err := nde.Validate(db)
	if err != nil {
		utils.AbortWithError(c, 500, err)
		return
	}

	if errData != nil {
		c.JSON(400, errData)
		return
	}

	err = nde.CommitFields(db, fields)
	if err != nil {
		utils.AbortWithError(c, 500, err)
		return
	}

	event.PublishDispatch(db, "node.change")

	c.JSON(200, nde)
}

func nodeOperationPut(c *gin.Context) {
	if demo.Blocked(c) {
		return
	}

	db := c.MustGet("db").(*database.Database)

	nodeId, ok := utils.ParseObjectId(c.Param("node_id"))
	if !ok {
		utils.AbortWithStatus(c, 400)
		return
	}

	operation := c.Param("operation")
	if operation != node.Restart {
		utils.AbortWithStatus(c, 400)
		return
	}

	nde, err := node.Get(db, nodeId)
	if err != nil {
		utils.AbortWithError(c, 500, err)
		return
	}

	nde.Operation = node.Restart

	errData, err := nde.Validate(db)
	if err != nil {
		utils.AbortWithError(c, 500, err)
		return
	}

	if errData != nil {
		c.JSON(400, errData)
		return
	}

	err = nde.CommitFields(db, set.NewSet("operation"))
	if err != nil {
		utils.AbortWithError(c, 500, err)
		return
	}

	c.JSON(200, nde)
}

func nodeDelete(c *gin.Context) {
	if demo.Blocked(c) {
		return
	}

	db := c.MustGet("db").(*database.Database)

	nodeId, ok := utils.ParseObjectId(c.Param("node_id"))
	if !ok {
		utils.AbortWithStatus(c, 400)
		return
	}

	err := node.Remove(db, nodeId)
	if err != nil {
		utils.AbortWithError(c, 500, err)
		return
	}

	event.PublishDispatch(db, "node.change")

	c.JSON(200, nil)
}

func nodeGet(c *gin.Context) {
	db := c.MustGet("db").(*database.Database)

	nodeId, ok := utils.ParseObjectId(c.Param("node_id"))
	if !ok {
		utils.AbortWithStatus(c, 400)
		return
	}

	nde, err := node.Get(db, nodeId)
	if err != nil {
		utils.AbortWithError(c, 500, err)
		return
	}

	if demo.IsDemo() {
		nde.RequestsMin = 32
		nde.Memory = 25.0
		nde.Load1 = 10.0
		nde.Load5 = 15.0
		nde.Load15 = 20.0
	}

	c.JSON(200, nde)
}

func nodesGet(c *gin.Context) {
	db := c.MustGet("db").(*database.Database)

	if c.Query("names") == "true" {
		zone, _ := utils.ParseObjectId(c.Query("zone"))

		query := &bson.M{
			"zone": zone,
		}

		nodes, err := node.GetAllHypervisors(db, query)
		if err != nil {
			utils.AbortWithError(c, 500, err)
			return
		}

		c.JSON(200, nodes)
	} else {
		page, _ := strconv.ParseInt(c.Query("page"), 10, 0)
		pageCount, _ := strconv.ParseInt(c.Query("page_count"), 10, 0)

		query := bson.M{}

		nodeId, ok := utils.ParseObjectId(c.Query("id"))
		if ok {
			query["_id"] = nodeId
		}

		name := strings.TrimSpace(c.Query("name"))
		if name != "" {
			query["name"] = &bson.M{
				"$regex":   fmt.Sprintf(".*%s.*", name),
				"$options": "i",
			}
		}

		zone, _ := utils.ParseObjectId(c.Query("zone"))
		if !zone.IsZero() {
			query["zone"] = zone
		}

		networkRole := c.Query("network_role")
		if networkRole != "" {
			query["network_roles"] = networkRole
		}

		types := []string{}
		notTypes := []string{}

		adminType := c.Query(node.Admin)
		switch adminType {
		case "true":
			types = append(types, node.Admin)
			break
		case "false":
			notTypes = append(notTypes, node.Admin)
			break
		}

		userType := c.Query(node.User)
		switch userType {
		case "true":
			types = append(types, node.User)
			break
		case "false":
			notTypes = append(notTypes, node.User)
			break
		}

		hypervisorType := c.Query(node.Hypervisor)
		switch hypervisorType {
		case "true":
			types = append(types, node.Hypervisor)
			break
		case "false":
			notTypes = append(notTypes, node.Hypervisor)
			break
		}

		typesQuery := bson.M{}
		if len(types) > 0 {
			typesQuery["$all"] = types
		}
		if len(notTypes) > 0 {
			typesQuery["$nin"] = notTypes
		}
		if len(types) > 0 || len(notTypes) > 0 {
			query["types"] = &typesQuery
		}

		nodes, count, err := node.GetAllPaged(db, &query, page, pageCount)
		if err != nil {
			utils.AbortWithError(c, 500, err)
			return
		}

		if demo.IsDemo() {
			for _, nde := range nodes {
				nde.RequestsMin = 32
				nde.Memory = 25.0
				nde.Load1 = 10.0
				nde.Load5 = 15.0
				nde.Load15 = 20.0
			}
		}

		data := &nodesData{
			Nodes: nodes,
			Count: count,
		}

		c.JSON(200, data)
	}
}
