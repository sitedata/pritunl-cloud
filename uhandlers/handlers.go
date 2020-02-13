package uhandlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/pritunl/pritunl-cloud/config"
	"github.com/pritunl/pritunl-cloud/constants"
	"github.com/pritunl/pritunl-cloud/middlewear"
	"github.com/pritunl/pritunl-cloud/requires"
	"github.com/pritunl/pritunl-cloud/static"
)

var (
	store      *static.Store
	fileServer http.Handler
)

func Register(engine *gin.Engine) {
	engine.Use(middlewear.Limiter)
	engine.Use(middlewear.Counter)
	engine.Use(middlewear.Recovery)

	dbGroup := engine.Group("")
	dbGroup.Use(middlewear.Database)

	sessGroup := dbGroup.Group("")
	sessGroup.Use(middlewear.SessionUser)

	authGroup := sessGroup.Group("")
	authGroup.Use(middlewear.AuthUser)

	csrfGroup := authGroup.Group("")
	csrfGroup.Use(middlewear.CsrfToken)

	orgGroup := csrfGroup.Group("")
	orgGroup.Use(middlewear.UserOrg)

	engine.NoRoute(middlewear.NotFound)

	engine.GET("/auth/state", authStateGet)
	dbGroup.POST("/auth/session", authSessionPost)
	dbGroup.POST("/auth/secondary", authSecondaryPost)
	dbGroup.GET("/auth/request", authRequestGet)
	dbGroup.GET("/auth/callback", authCallbackGet)
	engine.GET("/auth/u2f/app.json", authU2fAppGet)
	dbGroup.GET("/auth/u2f/register", authU2fRegisterGet)
	dbGroup.POST("/auth/u2f/register", authU2fRegisterPost)
	dbGroup.GET("/auth/u2f/sign", authU2fSignGet)
	dbGroup.POST("/auth/u2f/sign", authU2fSignPost)
	sessGroup.GET("/logout", logoutGet)
	sessGroup.GET("/logout_all", logoutAllGet)

	orgGroup.GET("/authority", authoritiesGet)
	orgGroup.GET("/authority/:authority_id", authorityGet)
	orgGroup.PUT("/authority/:authority_id", authorityPut)
	orgGroup.POST("/authority", authorityPost)
	orgGroup.DELETE("/authority", authoritiesDelete)
	orgGroup.DELETE("/authority/:authority_id", authorityDelete)

	orgGroup.GET("/balancer", balancersGet)
	orgGroup.GET("/balancer/:balancer_id", balancerGet)
	orgGroup.PUT("/balancer/:balancer_id", balancerPut)
	orgGroup.POST("/balancer", balancerPost)
	orgGroup.DELETE("/balancer", balancersDelete)
	orgGroup.DELETE("/balancer/:balancer_id", balancerDelete)

	orgGroup.GET("/certificate", certificatesGet)
	orgGroup.GET("/certificate/:cert_id", certificateGet)
	orgGroup.PUT("/certificate/:cert_id", certificatePut)
	orgGroup.POST("/certificate", certificatePost)
	orgGroup.DELETE("/certificate/:cert_id", certificateDelete)

	engine.GET("/check", checkGet)

	authGroup.GET("/csrf", csrfGet)

	orgGroup.GET("/datacenter", datacentersGet)

	csrfGroup.GET("/device", devicesGet)
	csrfGroup.PUT("/device/:device_id", devicePut)
	csrfGroup.DELETE("/device/:device_id", deviceDelete)
	csrfGroup.PUT("/device/:device_id/secondary", deviceU2fSecondaryPut)
	csrfGroup.GET("/device/:device_id/sign", deviceU2fSignGet)
	csrfGroup.POST("/device/:device_id/sign", deviceU2fSignPost)
	csrfGroup.GET("/device/:device_id/register", deviceU2fRegisterGet)
	csrfGroup.POST("/device/:device_id/register", deviceU2fRegisterPost)

	orgGroup.GET("/domain", domainsGet)

	orgGroup.GET("/disk", disksGet)
	orgGroup.GET("/disk/:disk_id", diskGet)
	orgGroup.PUT("/disk", disksPut)
	orgGroup.PUT("/disk/:disk_id", diskPut)
	orgGroup.POST("/disk", diskPost)
	orgGroup.DELETE("/disk", disksDelete)
	orgGroup.DELETE("/disk/:disk_id", diskDelete)

	csrfGroup.GET("/event", eventGet)

	orgGroup.GET("/firewall", firewallsGet)
	orgGroup.GET("/firewall/:firewall_id", firewallGet)
	orgGroup.PUT("/firewall/:firewall_id", firewallPut)
	orgGroup.POST("/firewall", firewallPost)
	orgGroup.DELETE("/firewall", firewallsDelete)
	orgGroup.DELETE("/firewall/:firewall_id", firewallDelete)

	orgGroup.GET("/image", imagesGet)
	orgGroup.GET("/image/:image_id", imageGet)
	orgGroup.PUT("/image/:image_id", imagePut)
	orgGroup.DELETE("/image", imagesDelete)
	orgGroup.DELETE("/image/:image_id", imageDelete)

	orgGroup.GET("/instance", instancesGet)
	orgGroup.PUT("/instance", instancesPut)
	orgGroup.GET("/instance/:instance_id", instanceGet)
	orgGroup.PUT("/instance/:instance_id", instancePut)
	orgGroup.POST("/instance", instancePost)
	orgGroup.DELETE("/instance", instancesDelete)
	orgGroup.DELETE("/instance/:instance_id", instanceDelete)

	csrfGroup.PUT("/license", licensePut)

	orgGroup.GET("/node", nodesGet)

	csrfGroup.GET("/organization", organizationsGet)

	csrfGroup.PUT("/theme", themePut)

	orgGroup.GET("/vpc", vpcsGet)
	orgGroup.GET("/vpc/:vpc_id", vpcGet)
	orgGroup.PUT("/vpc/:vpc_id", vpcPut)
	orgGroup.GET("/vpc/:vpc_id/routes", vpcRoutesGet)
	orgGroup.PUT("/vpc/:vpc_id/routes", vpcRoutesPut)
	orgGroup.POST("/vpc", vpcPost)
	orgGroup.DELETE("/vpc", vpcsDelete)
	orgGroup.DELETE("/vpc/:vpc_id", vpcDelete)

	orgGroup.GET("/zone", zonesGet)

	engine.GET("/robots.txt", middlewear.RobotsGet)

	if constants.Production {
		sessGroup.GET("/", staticIndexGet)
		engine.GET("/login", staticLoginGet)
		engine.GET("/logo.png", staticLogoGet)
		authGroup.GET("/static/*path", staticGet)
	} else {
		fs := gin.Dir(config.StaticTestingRoot, false)
		fileServer = http.FileServer(fs)

		sessGroup.GET("/", staticTestingGet)
		engine.GET("/login", staticTestingGet)
		engine.GET("/logo.png", staticTestingGet)
		authGroup.GET("/config.js", staticTestingGet)
		authGroup.GET("/build.js", staticTestingGet)
		authGroup.GET("/app/*path", staticTestingGet)
		authGroup.GET("/dist/*path", staticTestingGet)
		authGroup.GET("/styles/*path", staticTestingGet)
		authGroup.GET("/node_modules/*path", staticTestingGet)
		authGroup.GET("/jspm_packages/*path", staticTestingGet)
	}
}

func init() {
	module := requires.New("uhandlers")
	module.After("settings")

	module.Handler = func() (err error) {
		if constants.Production {
			store, err = static.NewStore(config.StaticRoot)
			if err != nil {
				return
			}
		}

		return
	}
}
