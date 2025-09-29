/*
 *  Copyright (c) 2024, WSO2 LLC. (http://www.wso2.org) All Rights Reserved.
 *
 *  Licensed under the Apache License, Version 2.0 (the "License");
 *  you may not use this file except in compliance with the License.
 *  You may obtain a copy of the License at
 *
 *  http://www.apache.org/licenses/LICENSE-2.0
 *
 *  Unless required by applicable law or agreed to in writing, software
 *  distributed under the License is distributed on an "AS IS" BASIS,
 *  WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 *  See the License for the specific language governing permissions and
 *  limitations under the License.
 *
 */
package managementserver

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/wso2/product-apim-tooling/apim-apk-agent/config"
	"github.com/wso2/product-apim-tooling/apim-apk-agent/internal/constants"
	logger "github.com/wso2/product-apim-tooling/apim-apk-agent/pkg/loggers"
	"github.com/wso2/product-apim-tooling/apim-apk-agent/pkg/utils"
	"gopkg.in/yaml.v2"
)

func init() {
}

// StartInternalServer starts the internal server
func StartInternalServer(port uint) {
	cpConfig, err := config.ReadConfigs()
	envLabel := []string{"Default"}
	if err == nil {
		envLabel = cpConfig.ControlPlane.EnvironmentLabels
	}
	gin.SetMode(gin.ReleaseMode)
	r := gin.Default()

	r.GET("/applications", func(c *gin.Context) {
		applicationList := GetAllApplications()
		c.JSON(http.StatusOK, ResolvedApplicationList{List: applicationList})
	})
	r.GET("/subscriptions", func(c *gin.Context) {
		subscriptionList := GetAllSubscriptions()
		c.JSON(http.StatusOK, SubscriptionList{List: subscriptionList})
	})
	r.GET("/applicationmappings", func(c *gin.Context) {
		applicationMappingList := GetAllApplicationMappings()
		c.JSON(http.StatusOK, ApplicationMappingList{List: applicationMappingList})
	})
	r.POST("/apis", func(c *gin.Context) {
		var event APICPEvent
		if err := c.ShouldBindJSON(&event); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		logger.LoggerMgtServer.Debugf("Recieved payload for endpoint /apis: %+v", event)
		if event.Event == DeleteEvent {
			logger.LoggerMgtServer.Infof("Delete event received with APIUUID: %s", event.API.APIUUID)
			payload := []map[string]interface{}{
				{
					"revisionUuid":       event.API.RevisionID,
					"name":               envLabel[0],
					"vhost":              event.API.Vhost,
					"displayOnDevportal": true,
				},
			}
			jsonPayload, err := json.Marshal(payload)
			logger.LoggerMgtServer.Debugf("Sending payload for revision undeploy: %+v", string(jsonPayload))
			if err != nil {
				logger.LoggerMgtServer.Errorf("Error while preparing payload to delete revision. Processed object: %+v", payload)
				c.JSON(http.StatusInternalServerError, err.Error())
				return
			}
			// Delete the api
			errorUndeployRevision := utils.DeleteAPIRevision(event.API.APIUUID, event.API.RevisionID, string(jsonPayload))
			if errorUndeployRevision != nil {
				logger.LoggerMgtServer.Errorf("Error while undeploying api revision. RevisionId: %s, API ID: %s . Sending error response to Adapter.", event.API.RevisionID, event.API.APIUUID)
				c.JSON(http.StatusServiceUnavailable, errorUndeployRevision.Error())
				return
			}
			c.JSON(http.StatusOK, map[string]string{"message": "Success"})
		} else {
			if strings.EqualFold(event.API.APIType, "rest") && event.API.Definition == "" {
				event.API.Definition = utils.OpenAPIDefaultYaml
			}
			if strings.EqualFold(event.API.APIType, "rest") {
				yaml, errJSONToYaml := JSONToYAML(event.API.Definition)
				if errJSONToYaml == nil {
					event.API.Definition = yaml
				}
			}
			apiYaml, definition, endpointsYaml := createAPIYaml(&event)
			deploymentContent := createDeployementYaml(event.API.Vhost)
			logger.LoggerMgtServer.Debugf("Created apiYaml : %s, \n\n\n created definition file: %s, \n\n\n created endpointYaml : %s", apiYaml, definition, endpointsYaml)
			definitionPath := fmt.Sprintf("%s-%s/Definitions/swagger.yaml", event.API.APIName, event.API.APIVersion)
			if strings.ToUpper(event.API.APIType) == "GRAPHQL" {
				definitionPath = fmt.Sprintf("%s-%s/Definitions/schema.graphql", event.API.APIName, event.API.APIVersion)
			}
			var zipFiles []utils.ZipFile
			logger.LoggerMgtServer.Debugf("endpoints yaml: %s", endpointsYaml)
			if endpointsYaml != "{}\n" {
				logger.LoggerMgtServer.Debugf("Creating zip file with endpoints")
				zipFiles = []utils.ZipFile{{
					Path:    fmt.Sprintf("%s-%s/api.yaml", event.API.APIName, event.API.APIVersion),
					Content: apiYaml,
				}, {
					Path:    fmt.Sprintf("%s-%s/endpoints.yaml", event.API.APIName, event.API.APIVersion),
					Content: endpointsYaml,
				}, {
					Path:    fmt.Sprintf("%s-%s/deployment_environments.yaml", event.API.APIName, event.API.APIVersion),
					Content: deploymentContent,
				}, {
					Path:    definitionPath,
					Content: definition,
				}}
			} else {
				logger.LoggerMgtServer.Debugf("Creating zip file without endpoints")
				zipFiles = []utils.ZipFile{{
					Path:    fmt.Sprintf("%s-%s/api.yaml", event.API.APIName, event.API.APIVersion),
					Content: apiYaml,
				}, {
					Path:    fmt.Sprintf("%s-%s/deployment_environments.yaml", event.API.APIName, event.API.APIVersion),
					Content: deploymentContent,
				}, {
					Path:    definitionPath,
					Content: definition,
				}}
			}
			var buf bytes.Buffer
			if err := utils.CreateZipFile(&buf, zipFiles); err != nil {
				logger.LoggerMgtServer.Errorf("Error while creating apim zip file for api uuid: %s. Error: %+v", event.API.APIUUID, err)
			}

			id, revisionID, err := utils.ImportAPI(fmt.Sprintf("admin-%s-%s.zip", event.API.APIName, event.API.APIVersion), &buf)
			if err != nil {
				logger.LoggerMgtServer.Errorf("Error while importing API. Sending error response to Adapter.")
				c.JSON(http.StatusServiceUnavailable, err.Error())
				return
			}
			c.JSON(http.StatusOK, map[string]string{"id": id, "revisionID": revisionID})
		}
	})
	publicKeyLocation, privateKeyLocation, _ := config.GetKeyLocations()
	r.RunTLS(fmt.Sprintf(":%d", port), publicKeyLocation, privateKeyLocation)
}

func createAPIYaml(apiCPEvent *APICPEvent) (string, string, string) {

	config, err := config.ReadConfigs()
	provider := "admin"
	if err == nil {
		provider = config.ControlPlane.Provider
	}
	context := removeVersionSuffix(apiCPEvent.API.BasePath, apiCPEvent.API.APIVersion)

	multiEndpoints := apiCPEvent.API.MultiEndpoints
	apimEndpints := []APIMEndpoint{}
	prodCount := 0
	sandCount := 0
	primaryProductionEndpointID := ""
	primarySandboxEndpointID := ""
	primaryProdcutionURL := ""
	primarySandboxURL := ""
	logger.LoggerMgtServer.Debugf("Production Multi-endpoint configs: %d", len(multiEndpoints.ProdEndpoints))
	for _, endpoint := range multiEndpoints.ProdEndpoints {
		prodCount++
		var endpointName string
		if prodCount == 1 {
			endpointName = "Default Production Endpoint"
		} else {
			endpointName = fmt.Sprintf("%d Production Endpoint", prodCount)
		}
		prodEndpoint := ""
		if endpoint.URL != "" {
			prodEndpoint = fmt.Sprintf("%s://%s", multiEndpoints.Protocol, endpoint.URL)
		}
		endpointUUID := uuid.New().String() + "--PRODUCTION"
		if prodCount == 1 {
			primaryProductionEndpointID = endpointUUID
			primaryProdcutionURL = prodEndpoint
		}
		apimEndpints = append(apimEndpints, APIMEndpoint{
			DeploymentStage: "PRODUCTION",
			EndpointUUID:    endpointUUID,
			EndpointName:    endpointName,
			EndpointConfig: APIMEndpointConfig{
				EndpointType: multiEndpoints.Protocol,
				ProductionEndpoints: Endpoints{
					URL: prodEndpoint,
				},
				EndpointSecurity: APIMEndpointSecurity{
					Production: SecurityConfig{
						Enabled:                          endpoint.SecurityEnabled,
						Type:                             endpoint.SecurityType,
						Username:                         endpoint.BasicUsername,
						Password:                         endpoint.BasicPassword,
						APIKeyIdentifier:                 endpoint.APIKeyName,
						APIKeyValue:                      endpoint.APIKeyValue,
						APIKeyIdentifierType:             endpoint.APIKeyIn,
						ConnectionTimeoutDuration:        -1.0,
						SocketTimeoutDuration:            -1.0,
						ConnectionRequestTimeoutDuration: -1.0,
					},
				},
			},
		})
	}
	logger.LoggerMgtServer.Debugf("Sandbox Multi-endpoint configs: %d", len(multiEndpoints.SandEndpoints))
	for _, endpoint := range multiEndpoints.SandEndpoints {
		sandCount++
		var endpointName string
		if sandCount == 1 {
			endpointName = "Default Sandbox Endpoint"
		} else {
			endpointName = fmt.Sprintf("%d Sandbox Endpoint", sandCount)
		}

		sandEndpoint := ""
		if endpoint.URL != "" {
			sandEndpoint = fmt.Sprintf("%s://%s", multiEndpoints.Protocol, endpoint.URL)
		}
		endpointUUID := uuid.New().String() + "--SANDBOX"
		if sandCount == 1 {
			primarySandboxEndpointID = endpointUUID
			primarySandboxURL = sandEndpoint
		}
		apimEndpints = append(apimEndpints, APIMEndpoint{
			DeploymentStage: "SANDBOX",
			EndpointUUID:    endpointUUID,
			EndpointName:    endpointName,
			EndpointConfig: APIMEndpointConfig{
				EndpointType: multiEndpoints.Protocol,
				SandboxEndpoints: Endpoints{
					URL: sandEndpoint,
				},
				EndpointSecurity: APIMEndpointSecurity{
					Sandbox: SecurityConfig{
						Enabled:                          endpoint.SecurityEnabled,
						Type:                             endpoint.SecurityType,
						Username:                         endpoint.BasicUsername,
						Password:                         endpoint.BasicPassword,
						APIKeyIdentifier:                 endpoint.APIKeyName,
						APIKeyValue:                      endpoint.APIKeyValue,
						APIKeyIdentifierType:             endpoint.APIKeyIn,
						ConnectionTimeoutDuration:        -1.0,
						SocketTimeoutDuration:            -1.0,
						ConnectionRequestTimeoutDuration: -1.0,
					},
				},
			},
		})
	}

	operations, scopes, operationsErr := extractOperations(*apiCPEvent, apimEndpints)
	if operationsErr != nil {
		logger.LoggerMgtServer.Errorf("Error occured while extracting operations from open API: %s, \nError: %+v", apiCPEvent.API.Definition, operationsErr)
		operations = []APIOperation{}
	}
	sandEndpoint := apiCPEvent.API.SandEndpoint
	if apiCPEvent.API.SandEndpoint != "" {
		sandEndpoint = fmt.Sprintf("%s://%s", apiCPEvent.API.EndpointProtocol, apiCPEvent.API.SandEndpoint)
	}
	prodEndpoint := apiCPEvent.API.ProdEndpoint
	logger.LoggerMgtServer.Infof("Sandbox Endpoint: %s, Production Endpoint: %s", sandEndpoint, prodEndpoint)
	if apiCPEvent.API.ProdEndpoint != "" {
		prodEndpoint = fmt.Sprintf("%s://%s", apiCPEvent.API.EndpointProtocol, apiCPEvent.API.ProdEndpoint)
	}
	authHeader := apiCPEvent.API.AuthHeader
	apiKeyHeader := apiCPEvent.API.APIKeyHeader
	apiType := "HTTP"
	if apiCPEvent.API.APIType == "GraphQL" {
		apiType = "GRAPHQL"
	}

	var subTypeConfiguration = make(map[string]interface{})
	if apiCPEvent.API.APISubType != "" && apiCPEvent.API.AIConfiguration.LLMProviderID != "" &&
		apiCPEvent.API.AIConfiguration.LLMProviderName != "" &&
		apiCPEvent.API.AIConfiguration.LLMProviderAPIVersion != "" {
		logger.LoggerMgtServer.Debugf("AI Configuration: %+v", apiCPEvent.API.AIConfiguration)
		subTypeConfiguration["subtype"] = apiCPEvent.API.APISubType
		subTypeConfiguration["_configuration"] = "{\"llmProviderId\":\"" +
			apiCPEvent.API.AIConfiguration.LLMProviderID + "\"}"
	}
	logger.LoggerMgtServer.Debugf("Subtype Configuration: %+v", subTypeConfiguration)
	sandboxSecType := apiCPEvent.API.SandEndpointSecurity.SecurityType
	if sandboxSecType == "" {
		sandboxSecType = "NONE"
	}
	prodSecType := apiCPEvent.API.ProdEndpointSecurity.SecurityType
	if prodSecType == "" {
		prodSecType = "NONE"
	}
	data := map[string]interface{}{
		"type":    "api",
		"version": "v4.6.0",
		"data": map[string]interface{}{
			"name":                         apiCPEvent.API.APIName,
			"context":                      context,
			"version":                      apiCPEvent.API.APIVersion,
			"organizationId":               apiCPEvent.API.Organization,
			"provider":                     provider,
			"lifeCycleStatus":              "CREATED",
			"responseCachingEnabled":       false,
			"cacheTimeout":                 300,
			"hasThumbnail":                 false,
			"isDefaultVersion":             apiCPEvent.API.IsDefaultVersion,
			"isRevision":                   false,
			"enableSchemaValidation":       false,
			"enableSubscriberVerification": false,
			"type":                         apiType,
			"transport":                    []string{"http", "https"},
			"endpointConfig": map[string]interface{}{
				"endpoint_type": apiCPEvent.API.EndpointProtocol,
				"sandbox_endpoints": map[string]interface{}{
					"url": sandEndpoint,
				},
				"production_endpoints": map[string]interface{}{
					"url": prodEndpoint,
				},
				"endpoint_security": map[string]interface{}{
					"sandbox": map[string]interface{}{
						"apiKeyValue":                      apiCPEvent.API.SandEndpointSecurity.APIKeyValue,
						"apiKeyIdentifier":                 apiCPEvent.API.SandEndpointSecurity.APIKeyName,
						"apiKeyIdentifierType":             "HEADER",
						"type":                             sandboxSecType,
						"username":                         apiCPEvent.API.SandEndpointSecurity.BasicUsername,
						"password":                         apiCPEvent.API.SandEndpointSecurity.BasicPassword,
						"enabled":                          apiCPEvent.API.SandEndpointSecurity.Enabled,
						"additionalProperties":             map[string]interface{}{},
						"customParameters":                 map[string]interface{}{},
						"connectionTimeoutDuration":        -1.0,
						"socketTimeoutDuration":            -1.0,
						"connectionRequestTimeoutDuration": -1.0,
					},
					"production": map[string]interface{}{
						"apiKeyValue":                      apiCPEvent.API.ProdEndpointSecurity.APIKeyValue,
						"apiKeyIdentifier":                 apiCPEvent.API.ProdEndpointSecurity.APIKeyName,
						"apiKeyIdentifierType":             "HEADER",
						"type":                             prodSecType,
						"username":                         apiCPEvent.API.ProdEndpointSecurity.BasicUsername,
						"password":                         apiCPEvent.API.ProdEndpointSecurity.BasicPassword,
						"enabled":                          apiCPEvent.API.ProdEndpointSecurity.Enabled,
						"additionalProperties":             map[string]interface{}{},
						"customParameters":                 map[string]interface{}{},
						"connectionTimeoutDuration":        -1.0,
						"socketTimeoutDuration":            -1.0,
						"connectionRequestTimeoutDuration": -1.0,
					},
				},
			},
			"policies":             []string{"Unlimited"},
			"gatewayType":          "wso2/apk",
			"gatewayVendor":        "wso2",
			"operations":           operations,
			"additionalProperties": createAdditionalProperties(apiCPEvent.API.APIProperties),
			"securityScheme":       apiCPEvent.API.SecurityScheme,
			"authorizationHeader":  authHeader,
			"apiKeyHeader":         apiKeyHeader,
			"scopes":               scopes,
		},
	}
	if len(subTypeConfiguration) > 0 {
		data["data"].(map[string]interface{})["subtypeConfiguration"] = subTypeConfiguration
		//data["data"].(map[string]interface{})["egress"] = true
	}
	// TODO when we start to process sandbox we need to have this if condition. For now we remove sandbox endpoint always.
	if apiCPEvent.API.SandEndpoint == "" {
		logger.LoggerMgtServer.Debug("Sandbox endpoint is empty. Removing sandbox endpoints from the endpoint config")
		delete(data["data"].(map[string]interface{})["endpointConfig"].(map[string]interface{}), "sandbox_endpoints")
	}
	if apiCPEvent.API.ProdEndpoint == "" {
		logger.LoggerMgtServer.Debug("Production endpoint is empty. Removing production endpoints from the endpoint config")
		delete(data["data"].(map[string]interface{})["endpointConfig"].(map[string]interface{}), "production_endpoints")
	}
	if apiCPEvent.API.CORSPolicy != nil {
		data["data"].(map[string]interface{})["corsConfiguration"] = map[string]interface{}{
			"corsConfigurationEnabled":      true,
			"accessControlAllowOrigins":     apiCPEvent.API.CORSPolicy.AccessControlAllowOrigins,
			"accessControlAllowCredentials": apiCPEvent.API.CORSPolicy.AccessControlAllowCredentials,
			"accessControlAllowHeaders":     apiCPEvent.API.CORSPolicy.AccessControlAllowHeaders,
			"accessControlAllowMethods":     apiCPEvent.API.CORSPolicy.AccessControlAllowMethods,
			"accessControlExposeHeaders":    apiCPEvent.API.CORSPolicy.AccessControlExposeHeaders,
		}
	}

	maxTps := make(map[string]interface{})

	// Handle Production fields
	if apiCPEvent.API.ProdAIRL != nil {
		logger.LoggerMgtServer.Infof("Production AIRL is not nil")
		maxTps["production"] = apiCPEvent.API.ProdAIRL.RequestCount
		maxTps["productionTimeUnit"] = strings.ToUpper(apiCPEvent.API.ProdAIRL.TimeUnit)

		tokenConfig := make(map[string]interface{})
		if apiCPEvent.API.ProdAIRL.PromptTokenCount != nil {
			tokenConfig["productionMaxPromptTokenCount"] = *apiCPEvent.API.ProdAIRL.PromptTokenCount
		}
		if apiCPEvent.API.ProdAIRL.CompletionTokenCount != nil {
			tokenConfig["productionMaxCompletionTokenCount"] = *apiCPEvent.API.ProdAIRL.CompletionTokenCount
		}
		if apiCPEvent.API.ProdAIRL.TotalTokenCount != nil {
			tokenConfig["productionMaxTotalTokenCount"] = *apiCPEvent.API.ProdAIRL.TotalTokenCount
		}
		if len(tokenConfig) > 0 {
			tokenConfig["isTokenBasedThrottlingEnabled"] = true
			maxTps["tokenBasedThrottlingConfiguration"] = tokenConfig
		}
	}

	// Handle Sandbox fields
	if apiCPEvent.API.SandAIRL != nil {
		logger.LoggerMgtServer.Infof("Sandbox AIRL is not nil")
		maxTps["sandbox"] = apiCPEvent.API.SandAIRL.RequestCount
		maxTps["sandboxTimeUnit"] = strings.ToUpper(apiCPEvent.API.SandAIRL.TimeUnit)

		// Add sandbox token-based throttling config
		if maxTps["tokenBasedThrottlingConfiguration"] == nil {
			maxTps["tokenBasedThrottlingConfiguration"] = make(map[string]interface{})
		}
		tokenConfig := maxTps["tokenBasedThrottlingConfiguration"].(map[string]interface{})
		if apiCPEvent.API.SandAIRL.PromptTokenCount != nil {
			tokenConfig["sandboxMaxPromptTokenCount"] = *apiCPEvent.API.SandAIRL.PromptTokenCount
		}
		if apiCPEvent.API.SandAIRL.CompletionTokenCount != nil {
			tokenConfig["sandboxMaxCompletionTokenCount"] = *apiCPEvent.API.SandAIRL.CompletionTokenCount
		}
		if apiCPEvent.API.SandAIRL.TotalTokenCount != nil {
			tokenConfig["sandboxMaxTotalTokenCount"] = *apiCPEvent.API.SandAIRL.TotalTokenCount
		}
	}
	if len(maxTps) > 0 {
		data["data"].(map[string]interface{})["maxTps"] = maxTps
	}
	logger.LoggerMgtServer.Debugf("Prepared yaml : %+v", data)
	definition := apiCPEvent.API.Definition
	if strings.EqualFold(apiCPEvent.API.APIType, "rest") {
		// Process OpenAPI and set required values
		openAPI, errConvertYaml := ConvertYAMLToMap(definition)
		if errConvertYaml == nil {
			if paths, ok := openAPI["paths"].(map[interface{}]interface{}); ok {
				for path, pathContent := range paths {
					if pathContentMap, ok := pathContent.(map[interface{}]interface{}); ok {
						for verb, verbContent := range pathContentMap {
							for _, operation := range operations {
								if strings.EqualFold(path.(string), operation.Target) && strings.EqualFold(verb.(string), operation.Verb) {
									if verbContentMap, ok := verbContent.(map[interface{}]interface{}); ok {
										if len(operation.Scopes) > 0 {
											verbContentMap["security"] = []map[string][]string{
												{
													"default": operation.Scopes,
												},
											}
										}
										verbContentMap["x-auth-type"] = "Application & Application User"
									}
									break
								}
							}
						}
					}
				}
			}
			scopesForOpenAPIComponents := map[string]string{}
			for _, scopeWrapper := range scopes {
				scopesForOpenAPIComponents[scopeWrapper.Scope.Name] = ""
			}

			components, ok := openAPI["components"].(map[interface{}]interface{})
			if !ok {
				components = make(map[interface{}]interface{})
			}
			securitySchemes, ok := components["securitySchemes"].(map[interface{}]interface{})
			if !ok {
				securitySchemes = make(map[interface{}]interface{})
			}

			securitySchemes["default"] = map[interface{}]interface{}{
				"type": "oauth2",
				"flows": map[interface{}]interface{}{
					"implicit": map[interface{}]interface{}{
						"authorizationUrl":  "https://test.com",
						"scopes":            scopesForOpenAPIComponents,
						"x-scopes-bindings": scopesForOpenAPIComponents,
					},
				},
			}

			components["securitySchemes"] = securitySchemes
			openAPI["components"] = components

			yamlBytes, err := yaml.Marshal(&openAPI)
			if err != nil {
				logger.LoggerMgtServer.Errorf("Error while converting openAPI struct to yaml content. openAPI struct: %+v", openAPI)
			} else {
				logger.LoggerMgtServer.Debugf("Created openAPI yaml: %s", string(yamlBytes))
				definition = string(yamlBytes)
			}
		}
	}

	dataArr := make([]map[string]interface{}, 0, len(apimEndpints))

	for _, e := range apimEndpints {
		// Build the top-level map for this endpoint
		endpointMap := map[string]interface{}{
			"id":              e.EndpointUUID,
			"name":            e.EndpointName,
			"deploymentStage": e.DeploymentStage, // e.g. "PRODUCTION" or "SANDBOX"
		}

		// Build the endpointConfig sub-map
		configMap := map[string]interface{}{
			"endpoint_type": e.EndpointConfig.EndpointType, // e.g. "http" or "https"
		}

		// Depending on PRODUCTION or SANDBOX, fill the right endpoints key
		if e.DeploymentStage == "PRODUCTION" {
			configMap["production_endpoints"] = map[string]interface{}{
				"url": e.EndpointConfig.ProductionEndpoints.URL,
			}
		} else if e.DeploymentStage == "SANDBOX" {
			configMap["sandbox_endpoints"] = map[string]interface{}{
				"url": e.EndpointConfig.SandboxEndpoints.URL,
			}
		}

		// Build endpoint_security sub-map
		endpointSecurityMap := map[string]interface{}{}

		// Production or Sandbox security
		if e.DeploymentStage == "PRODUCTION" {
			sec := e.EndpointConfig.EndpointSecurity.Production
			endpointSecurityMap["production"] = map[string]interface{}{
				"enabled":                          sec.Enabled,
				"type":                             sec.Type,
				"apiKeyIdentifier":                 sec.APIKeyIdentifier,
				"apiKeyValue":                      sec.APIKeyValue,
				"apiKeyIdentifierType":             sec.APIKeyIdentifierType,
				"username":                         sec.Username,
				"customParameters":                 "{}", // Or map[string]string{}
				"connectionTimeoutDuration":        sec.ConnectionTimeoutDuration,
				"connectionRequestTimeoutDuration": sec.ConnectionRequestTimeoutDuration,
				"socketTimeoutDuration":            sec.SocketTimeoutDuration,
				"grantType":                        "",
				"tokenUrl":                         "",
				"proxyConfigs": map[string]interface{}{
					"proxyEnabled":  "",
					"proxyHost":     "",
					"proxyPort":     "",
					"proxyUsername": "",
					"proxyPassword": "",
					"proxyProtocol": "",
				},
			}
		} else if e.DeploymentStage == "SANDBOX" {
			sec := e.EndpointConfig.EndpointSecurity.Sandbox
			endpointSecurityMap["sandbox"] = map[string]interface{}{
				"enabled":                          sec.Enabled,
				"type":                             sec.Type,
				"apiKeyIdentifier":                 sec.APIKeyIdentifier,
				"apiKeyValue":                      sec.APIKeyValue,
				"apiKeyIdentifierType":             sec.APIKeyIdentifierType,
				"username":                         sec.Username,
				"customParameters":                 "{}",
				"connectionTimeoutDuration":        sec.ConnectionTimeoutDuration,
				"connectionRequestTimeoutDuration": sec.ConnectionRequestTimeoutDuration,
				"socketTimeoutDuration":            sec.SocketTimeoutDuration,
				"grantType":                        "",
				"tokenUrl":                         "",
				"proxyConfigs": map[string]interface{}{
					"proxyEnabled":  "",
					"proxyHost":     "",
					"proxyPort":     "",
					"proxyUsername": "",
					"proxyPassword": "",
					"proxyProtocol": "",
				},
			}
		}
		endpointSecurityMap["customParameters"] = "null"

		// Attach endpoint_security to configMap
		configMap["endpoint_security"] = endpointSecurityMap
		logger.LoggerMgtServer.Debugf("Endpoint Security Map: %+v", endpointSecurityMap)

		// Put endpointConfig in the main endpointMap
		endpointMap["endpointConfig"] = configMap

		// Append this endpoint to dataArr
		dataArr = append(dataArr, endpointMap)
	}

	var endpointsData map[string]interface{}
	logger.LoggerMgtServer.Debugf("Prod Count: %d || Sand Count: %d", prodCount, sandCount)
	if prodCount > 1 || sandCount > 1 {
		endpointsData = map[string]interface{}{
			"type":    "endpoints",
			"version": "v4.6.0",
			"data":    dataArr,
		}
	}
	logger.LoggerMgtServer.Debugf("\n\nSandbox Endpoint Sec Config: %+v\n\n", apiCPEvent.API.SandEndpointSecurity)
	logger.LoggerMgtServer.Debugf("\n\nProd Endpoint Sec Config: %+v\n\n", apiCPEvent.API.ProdEndpointSecurity)

	if primaryProductionEndpointID != "" || primarySandboxEndpointID != "" {
		logger.LoggerMgtServer.Debugf("Multi-endpoint config enabled")
		logger.LoggerMgtServer.Debugf("\n\nPrimary Production Endpoint ID: %s || Primary Sandbox Endpoint ID: %s\n\n", primaryProductionEndpointID, primarySandboxEndpointID)
		logger.LoggerMgtServer.Debugf("Primary Production URL: %s", primaryProdcutionURL)
		logger.LoggerMgtServer.Debugf("Primary Sandbox URL: %s", primarySandboxURL)

		data["data"].(map[string]interface{})["primaryProductionEndpointId"] = primaryProductionEndpointID
		data["data"].(map[string]interface{})["primarySandboxEndpointId"] = primarySandboxEndpointID
		data["data"].(map[string]interface{})["endpointImplementationType"] = "ENDPOINT"
		data["data"].(map[string]interface{})["endpointConfig"] = map[string]interface{}{
			"endpoint_type": apiCPEvent.API.EndpointProtocol,
			"sandbox_endpoints": map[string]interface{}{
				"url": primarySandboxURL,
			},
			"production_endpoints": map[string]interface{}{
				"url": primaryProdcutionURL,
			},
			"endpoint_security": map[string]interface{}{
				"sandbox": map[string]interface{}{
					"apiKeyValue":                      apiCPEvent.API.SandEndpointSecurity.APIKeyValue,
					"apiKeyIdentifier":                 apiCPEvent.API.SandEndpointSecurity.APIKeyName,
					"apiKeyIdentifierType":             "HEADER",
					"type":                             apiCPEvent.API.SandEndpointSecurity.SecurityType,
					"username":                         apiCPEvent.API.SandEndpointSecurity.BasicUsername,
					"password":                         apiCPEvent.API.SandEndpointSecurity.BasicPassword,
					"enabled":                          apiCPEvent.API.SandEndpointSecurity.Enabled,
					"additionalProperties":             map[string]interface{}{},
					"customParameters":                 map[string]interface{}{},
					"connectionTimeoutDuration":        -1.0,
					"socketTimeoutDuration":            -1.0,
					"connectionRequestTimeoutDuration": -1.0,
				},
				"production": map[string]interface{}{
					"apiKeyValue":                      apiCPEvent.API.ProdEndpointSecurity.APIKeyValue,
					"apiKeyIdentifier":                 apiCPEvent.API.ProdEndpointSecurity.APIKeyName,
					"apiKeyIdentifierType":             "HEADER",
					"type":                             apiCPEvent.API.ProdEndpointSecurity.SecurityType,
					"username":                         apiCPEvent.API.ProdEndpointSecurity.BasicUsername,
					"password":                         apiCPEvent.API.ProdEndpointSecurity.BasicPassword,
					"enabled":                          apiCPEvent.API.ProdEndpointSecurity.Enabled,
					"additionalProperties":             map[string]interface{}{},
					"customParameters":                 map[string]interface{}{},
					"connectionTimeoutDuration":        -1.0,
					"socketTimeoutDuration":            -1.0,
					"connectionRequestTimeoutDuration": -1.0,
				},
			},
		}
	}

	var requestOperationPolicies []OperationPolicy
	if apiCPEvent.API.AIModelBasedRoundRobin != nil && apiCPEvent.API.APIType != "GraphQL" {
		aiModelBasedRoundRobin := apiCPEvent.API.AIModelBasedRoundRobin
		logger.LoggerMgtServer.Debugf("AIModelBasedRoundRobin : %+v", aiModelBasedRoundRobin)
		wrr := ModelBasedRoundRobinConfig{
			Production:      convertAIModelWeightsToModelConfigs(aiModelBasedRoundRobin.ProductionModels, apimEndpints, true),
			Sandbox:         convertAIModelWeightsToModelConfigs(aiModelBasedRoundRobin.SandboxModels, apimEndpints, false),
			SuspendDuration: fmt.Sprintf("%d", aiModelBasedRoundRobin.OnQuotaExceedSuspendDuration),
		}
		jsonBytes, err := json.Marshal(wrr)
		if err != nil {
			logger.LoggerMgtServer.Errorf("Error marshaling WeightedRoundRobinConfigs to JSON: %+v", err)
		}
		jsonStr := string(jsonBytes)
		singleQuoted := strings.ReplaceAll(jsonStr, `"`, `'`)
		apiPolicy := OperationPolicy{
			PolicyName:    constants.ModelWeightedRoundRobin,
			PolicyVersion: constants.V1,
			PolicyType:    constants.CommonType,
			Parameters: WeightedRoundRobinConfigs{
				WeightedRoundRobinConfigs: singleQuoted,
			},
		}
		requestOperationPolicies = append(requestOperationPolicies, apiPolicy)
	}
	data["data"].(map[string]interface{})["apiPolicies"] = OperationPolicies{
		Request: requestOperationPolicies,
	}

	logger.LoggerMgtServer.Debugf("API Yaml: %+v", data)
	yamlBytes, _ := yaml.Marshal(data)
	logger.LoggerMgtServer.Debugf("Endpoint Yaml: %v", endpointsData)
	endpointBytes, _ := yaml.Marshal(endpointsData)
	return string(yamlBytes), definition, string(endpointBytes)
}

func createDeployementYaml(vhost string) string {
	config, err := config.ReadConfigs()
	envLabel := []string{"Default"}
	if err == nil {
		envLabel = config.ControlPlane.EnvironmentLabels
	}
	deploymentEnvData := []map[string]interface{}{}
	for _, label := range envLabel {
		deploymentEnvData = append(deploymentEnvData, map[string]interface{}{
			"displayOnDevportal":    true,
			"deploymentEnvironment": label,
			"deploymentVhost":       vhost,
		})
	}
	data := map[string]interface{}{
		"type":    "deployment_environments",
		"version": "v4.3.0",
		"data":    deploymentEnvData,
	}

	yamlBytes, _ := yaml.Marshal(data)
	return string(yamlBytes)
}

// APIOperation represents the desired struct format for each API operation
type APIOperation struct {
	ID                string            `yaml:"id"`
	Target            string            `yaml:"target"`
	Verb              string            `yaml:"verb"`
	AuthType          string            `yaml:"authType"`
	ThrottlingPolicy  string            `yaml:"throttlingPolicy"`
	Scopes            []string          `yaml:"scopes"`
	UsedProductIDs    []string          `yaml:"usedProductIds"`
	OperationPolicies OperationPolicies `yaml:"operationPolicies"`
}

// OperationPolicies contains the request, response and fault policies for an operation
type OperationPolicies struct {
	Request  []OperationPolicy `yaml:"request"`
	Response []OperationPolicy `yaml:"response"`
	Fault    []string          `yaml:"fault"`
}

// OperationPolicy represents the desired struct format for an Operation Policy
type OperationPolicy struct {
	PolicyName    string           `yaml:"policyName"`
	PolicyVersion string           `yaml:"policyVersion"`
	PolicyID      string           `yaml:"policyId,omitempty"`
	PolicyType    string           `yaml:"policyType,omitempty"`
	Parameters    FilterParameters `yaml:"parameters"`
}

// FilterParameters interface is used to define the type of parameters that can be used in an operation policy.
type FilterParameters interface {
	isFilterParameters()
}

func (m WeightedRoundRobinConfigs) isFilterParameters() {}

// WeightedRoundRobinConfigs holds any additional parameter data for a RequestPolicy
type WeightedRoundRobinConfigs struct {
	WeightedRoundRobinConfigs string `yaml:"weightedRoundRobinConfigs"`
}

func (m ModelBasedRoundRobinConfig) isFilterParameters() {}

// ModelConfig holds the configuration details of a model
type ModelConfig struct {
	Model      string `json:"model" yaml:"model"`
	EndpointID string `json:"endpointId" yaml:"endpointId"`
	Weight     int    `json:"weight" yaml:"weight"`
}

// ModelBasedRoundRobinConfig holds the configuration details of the transformer
type ModelBasedRoundRobinConfig struct {
	Production      []ModelConfig `json:"production" yaml:"production"`
	Sandbox         []ModelConfig `json:"sandbox" yaml:"sandbox"`
	SuspendDuration string        `json:"suspendDuration" yaml:"suspendDuration"`
}

func (h Header) isFilterParameters() {}

// Header contains the request and response header modifier information
type Header struct {
	Name  string `json:"headerName" yaml:"headerName"`
	Value string `json:"headerValue,omitempty" yaml:"headerValue,omitempty"`
}

// RedirectRequest contains the url to send the redirected request
type RedirectRequest struct {
	URL string `json:"url"`
}

func (r RedirectRequest) isFilterParameters() {}

// MirrorRequest contains the url to mirror the request to
type MirrorRequest struct {
	URL string `json:"url"`
}

func (m MirrorRequest) isFilterParameters() {}

// OpenAPIPaths represents the structure of the OpenAPI specification YAML file
type OpenAPIPaths struct {
	Paths map[string]map[string]interface{} `yaml:"paths"`
}

// Operation represents the structure of an operation within the OpenAPI specification
type Operation struct {
	XAuthType        string `yaml:"x-auth-type"`
	XThrottlingTier  string `yaml:"x-throttling-tier"`
	XWSO2AppSecurity struct {
		SecurityTypes []string `yaml:"security-types"`
		Optional      bool     `yaml:"optional"`
	} `yaml:"x-wso2-application-security"`
}

// AdditionalProperty represents additional properties of the API
type AdditionalProperty struct {
	Name    string
	Value   string
	Display bool
}

// ScopeWrapper to hold scope sonfigs
type ScopeWrapper struct {
	Scope  Scope `yaml:"scope"`
	Shared bool  `yaml:"shared"`
}

// Scope to hold scope config
type Scope struct {
	Name        string   `yaml:"name"`
	DisplayName string   `yaml:"displayName"`
	Description string   `yaml:"description"`
	Bindings    []string `yaml:"bindings"`
}

func convertAIModelWeightsToModelConfigs(weights []AIModelWeight, apimEndpoints []APIMEndpoint, isProd bool) []ModelConfig {
	var configs []ModelConfig
	for _, weight := range weights {
		var endpointID string
		for _, endpoint := range apimEndpoints {
			if endpoint.EndpointConfig.ProductionEndpoints.URL == weight.Endpoint {
				endpointID = endpoint.EndpointUUID
				break
			}
			if endpoint.EndpointConfig.SandboxEndpoints.URL == weight.Endpoint {
				endpointID = endpoint.EndpointUUID
				break
			}
		}
		configs = append(configs, ModelConfig{
			Model:      weight.Model,
			EndpointID: endpointID,
			Weight:     weight.Weight,
		})
	}
	return configs
}

func extractOperations(event APICPEvent, apimEndpoints []APIMEndpoint) ([]APIOperation, []ScopeWrapper, error) {
	var apiOperations []APIOperation
	var requestOperationPolicies []OperationPolicy
	var responseOperationPolicies []OperationPolicy
	scopewrappers := map[string]ScopeWrapper{}
	if strings.ToUpper(event.API.APIType) == "GRAPHQL" {
		for _, operation := range event.API.Operations {
			apiOp := APIOperation{
				Target:           operation.Path,
				Verb:             operation.Verb,
				AuthType:         "Application & Application User",
				ThrottlingPolicy: "Unlimited",
			}
			apiOperations = append(apiOperations, apiOp)
		}
	} else if strings.ToUpper(event.API.APIType) == "REST" {
		var openAPIPaths OpenAPIPaths
		openAPI := event.API.Definition
		if err := yaml.Unmarshal([]byte(openAPI), &openAPIPaths); err != nil {
			return nil, nil, err
		}

		for path, operations := range openAPIPaths.Paths {
			for verb := range operations {
				ptrToOperationFromDP := findMatchingAPKOperation(path, verb, event.API.Operations)
				if ptrToOperationFromDP == nil {
					continue
				}
				operationFromDP := *ptrToOperationFromDP
				scopes := operationFromDP.Scopes
				for _, scope := range scopes {
					scopewrappers[scope] = ScopeWrapper{
						Scope: Scope{
							Name:        scope,
							DisplayName: scope,
							Description: scope,
						},
						Shared: false,
					}
				}
				aiModelBasedRoundRobin := operationFromDP.AIModelBasedRoundRobin
				if aiModelBasedRoundRobin != nil {
					operationPolicy := OperationPolicy{
						PolicyName:    constants.ModelWeightedRoundRobin,
						PolicyVersion: constants.V1,
						Parameters: ModelBasedRoundRobinConfig{
							Production:      convertAIModelWeightsToModelConfigs(aiModelBasedRoundRobin.ProductionModels, apimEndpoints, true),
							Sandbox:         convertAIModelWeightsToModelConfigs(aiModelBasedRoundRobin.SandboxModels, apimEndpoints, false),
							SuspendDuration: fmt.Sprintf("%d", aiModelBasedRoundRobin.OnQuotaExceedSuspendDuration),
						},
					}
					requestOperationPolicies = append(requestOperationPolicies, operationPolicy)
				}
				// Process filters
				for _, operationLevelFilter := range operationFromDP.Filters {
					switch filter := operationLevelFilter.(type) {
					// Header modification policies
					case *APKHeaders:
						requestHeaders := filter.RequestHeaders
						// Add headers
						if requestHeaders.AddHeaders != nil && len(requestHeaders.AddHeaders) > 0 {
							logger.LoggerMgtServer.Debugf("Processing request filter for header addition")
							for _, requestHeader := range requestHeaders.AddHeaders {
								operationPolicy := OperationPolicy{
									PolicyName:    constants.AddHeader,
									PolicyVersion: constants.V1,
									Parameters: Header{
										Name:  requestHeader.Name,
										Value: requestHeader.Value,
									},
								}
								requestOperationPolicies = append(requestOperationPolicies, operationPolicy)
							}
						}

						// Remove headers
						if requestHeaders.RemoveHeaders != nil && len(requestHeaders.RemoveHeaders) > 0 {
							logger.LoggerMgtServer.Debugf("Processing request filter for header removal")
							for _, requestHeader := range requestHeaders.RemoveHeaders {
								operationPolicy := OperationPolicy{
									PolicyName:    constants.RemoveHeader,
									PolicyVersion: constants.V1,
									Parameters: Header{
										Name: requestHeader,
									},
								}
								requestOperationPolicies = append(responseOperationPolicies, operationPolicy)
							}
						}

						responseHeaders := filter.ResponseHeaders
						// Add headers
						if responseHeaders.AddHeaders != nil && len(responseHeaders.AddHeaders) > 0 {
							logger.LoggerMgtServer.Debugf("Processing response filter for header addition")
							for _, responseHeader := range responseHeaders.AddHeaders {
								operationPolicy := OperationPolicy{
									PolicyName:    constants.AddHeader,
									PolicyVersion: constants.V1,
									Parameters: Header{
										Name:  responseHeader.Name,
										Value: responseHeader.Value,
									},
								}
								responseOperationPolicies = append(responseOperationPolicies, operationPolicy)
							}
						}

						// Remove headers
						if responseHeaders.RemoveHeaders != nil && len(responseHeaders.RemoveHeaders) > 0 {
							logger.LoggerMgtServer.Debugf("Processing response filter for header removal")
							for _, responseHeader := range responseHeaders.RemoveHeaders {
								operationPolicy := OperationPolicy{
									PolicyName:    constants.RemoveHeader,
									PolicyVersion: constants.V1,
									Parameters: Header{
										Name: responseHeader,
									},
								}
								responseOperationPolicies = append(responseOperationPolicies, operationPolicy)
							}
						}
					// Mirror request
					case *APKMirrorRequest:
						logger.LoggerMgtServer.Debugf("Processing request filter for request mirroring")
						for _, url := range filter.URLs {
							operationPolicy := OperationPolicy{
								PolicyName:    constants.MirrorRequest,
								PolicyVersion: constants.V1,
								Parameters: MirrorRequest{
									URL: url,
								},
							}
							requestOperationPolicies = append(requestOperationPolicies, operationPolicy)
						}

					// Redirect request
					case *APKRedirectRequest:
						logger.LoggerMgtServer.Debugf("Processing request filter for request redirection")
						operationPolicy := OperationPolicy{
							PolicyName:    constants.RedirectRequest,
							PolicyVersion: constants.V1,
							Parameters: MirrorRequest{
								URL: filter.URL,
							},
						}
						requestOperationPolicies = append(requestOperationPolicies, operationPolicy)

					default:
						logger.LoggerMgtServer.Errorf("Unknown filter type ")
					}
				}

				apiOp := APIOperation{
					Target:           path,
					Verb:             verb,
					AuthType:         "Application & Application User",
					ThrottlingPolicy: "Unlimited",
					Scopes:           scopes,
					OperationPolicies: OperationPolicies{
						Request:  requestOperationPolicies,
						Response: responseOperationPolicies,
					},
				}
				apiOperations = append(apiOperations, apiOp)
			}
		}
		var scopeWrapperSlice []ScopeWrapper
		for _, value := range scopewrappers {
			scopeWrapperSlice = append(scopeWrapperSlice, value)
		}
		return apiOperations, scopeWrapperSlice, nil
	}
	return []APIOperation{}, []ScopeWrapper{}, nil
}

func findMatchingAPKOperation(path string, verb string, operations []OperationFromDP) *OperationFromDP {
	for _, operationFromDP := range operations {
		if strings.EqualFold(operationFromDP.Verb, verb) {
			path = processOpenAPIPath(path)
			if matchRegex(operationFromDP.Path, path) {
				return &operationFromDP
			}
		}
	}
	return nil
}

func removeVersionSuffix(str1, str2 string) string {
	if strings.HasSuffix(str1, str2) {
		return strings.TrimSuffix(str1, fmt.Sprintf("/%s", str2))
	}
	return str1
}

// createAdditionalProperties creates additional property elements from map
func createAdditionalProperties(data map[string]string) []AdditionalProperty {
	var properties []AdditionalProperty
	for key, value := range data {
		entry := AdditionalProperty{
			Name:    key,
			Value:   value,
			Display: false,
		}
		properties = append(properties, entry)
	}
	return properties
}

func matchRegex(regexStr string, targetStr string) bool {
	regexPattern, err := regexp.Compile(regexStr)
	if err != nil {
		fmt.Println("Error compiling regex:", err)
		return false
	}
	return regexPattern.MatchString(targetStr)
}

func processOpenAPIPath(path string) string {
	re := regexp.MustCompile(`{[^}]+}`)
	return re.ReplaceAllString(path, "hardcode")
}

// ConvertYAMLToMap converts a YAML string to a map[string]interface{}
func ConvertYAMLToMap(yamlString string) (map[string]interface{}, error) {
	var yamlData map[string]interface{}
	err := yaml.Unmarshal([]byte(yamlString), &yamlData)
	if err != nil {
		logger.LoggerMgtServer.Errorf("Error while converting openAPI yaml to map: Error: %+v. \n openAPI yaml", err)
		return nil, err
	}
	return yamlData, nil
}

// JSONToYAML convert json string to yaml
func JSONToYAML(jsonString string) (string, error) {
	// Convert JSON string to map[string]interface{}
	var jsonData map[string]interface{}
	err := json.Unmarshal([]byte(jsonString), &jsonData)
	if err != nil {
		return "", err
	}

	// Convert map[string]interface{} to YAML
	yamlBytes, err := yaml.Marshal(jsonData)
	if err != nil {
		return "", err
	}

	// Convert YAML bytes to string
	yamlString := string(yamlBytes)

	return yamlString, nil
}
