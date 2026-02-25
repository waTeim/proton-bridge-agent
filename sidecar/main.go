// Proton Bridge Sidecar — REST API for bridge login and credential management.
//
// @title           Proton Bridge Sidecar API
// @version         1.0
// @description     REST API for managing Proton Mail Bridge login and retrieving bridge credentials.
// @host            localhost:4209
// @BasePath        /
package main

import (
	"log/slog"
	"os"

	"github.com/gin-gonic/gin"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"

	_ "proton-bridge-sidecar/docs"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

	bc := newBridgeClient()
	setBridgeClientGlobal(bc)

	r := gin.Default()

	v1 := r.Group("/api/v1")
	{
		v1.POST("/credentials", PostCredentials)
		v1.GET("/credentials", GetCredentials)
		v1.GET("/credentials/status", GetCredentialsStatus)
		v1.PUT("/credentials", PutCredentials)
		v1.DELETE("/credentials", DeleteCredentials)
	}

	r.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))

	port := os.Getenv("PORT")
	if port == "" {
		port = "4209"
	}

	slog.Info("starting sidecar", "port", port)
	if err := r.Run(":" + port); err != nil {
		slog.Error("server error", "error", err)
		os.Exit(1)
	}
}
