package main

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

type credentialsRequest struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

// PostCredentials starts an async login with the provided credentials.
//
// @Summary      Start async login
// @Description  Initiates login against the bridge gRPC API. Returns 202 immediately; poll GET /api/v1/credentials/status for result.
// @Tags         credentials
// @Accept       json
// @Produce      json
// @Param        body  body      credentialsRequest  true  "Proton account credentials"
// @Success      202   {object}  map[string]string
// @Failure      400   {object}  map[string]string
// @Failure      409   {object}  map[string]string
// @Router       /api/v1/credentials [post]
func PostCredentials(c *gin.Context) {
	var req credentialsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	bc := getBridgeClient()
	if err := bc.StartLogin(req.Username, req.Password); err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusAccepted, gin.H{"message": "login started"})
}

// GetCredentials returns the bridge-generated IMAP credentials for the connected account.
//
// @Summary      Get IMAP credentials
// @Description  Returns the bridge-generated IMAP username and password. The password is a local bridge credential, not the Proton account password.
// @Tags         credentials
// @Produce      json
// @Success      200  {object}  map[string]string  "username and password"
// @Failure      404  {object}  map[string]string
// @Router       /api/v1/credentials [get]
func GetCredentials(c *gin.Context) {
	bc := getBridgeClient()
	username, password, ok := bc.GetIMAPCredentials()
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "not logged in"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"username": username, "password": password})
}

// GetCredentialsStatus returns the current login state.
//
// @Summary      Get login status
// @Description  Returns the current state: pending (login in progress), connected, reconnecting, error, or idle.
// @Tags         credentials
// @Produce      json
// @Success      200  {object}  map[string]string
// @Router       /api/v1/credentials/status [get]
func GetCredentialsStatus(c *gin.Context) {
	bc := getBridgeClient()
	state, msg := bc.GetStatus()
	c.JSON(http.StatusOK, gin.H{
		"state":   state,
		"message": msg,
	})
}

// PutCredentials logs out and re-logs in with new credentials.
//
// @Summary      Re-login
// @Description  Logs out the current user and starts an async login with the supplied credentials.
// @Tags         credentials
// @Accept       json
// @Produce      json
// @Param        body  body      credentialsRequest  true  "New credentials"
// @Success      202   {object}  map[string]string
// @Failure      400   {object}  map[string]string
// @Router       /api/v1/credentials [put]
func PutCredentials(c *gin.Context) {
	var req credentialsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	bc := getBridgeClient()
	bc.Logout()
	if err := bc.StartLogin(req.Username, req.Password); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusAccepted, gin.H{"message": "re-login started"})
}

// DeleteCredentials logs out the current user.
//
// @Summary      Logout
// @Description  Logs out the current bridge user and clears stored credentials.
// @Tags         credentials
// @Produce      json
// @Success      200  {object}  map[string]string
// @Router       /api/v1/credentials [delete]
func DeleteCredentials(c *gin.Context) {
	bc := getBridgeClient()
	bc.Logout()
	c.JSON(http.StatusOK, gin.H{"message": "logged out"})
}
