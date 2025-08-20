/*
*  Copyright (c) 2025 WSO2 LLC. (http://www.wso2.org) All Rights Reserved.
*
*  WSO2 LLC. licenses this file to you under the Apache License,
*  Version 2.0 (the "License"); you may not use this file except
*  in compliance with the License.
*  You may obtain a copy of the License at
*
*    http://www.apache.org/licenses/LICENSE-2.0
*
* Unless required by applicable law or agreed to in writing,
* software distributed under the License is distributed on an
* "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
* KIND, either express or implied.  See the License for the
* specific language governing permissions and limitations
* under the License.
 */

package apim

// MCPServerList : MCP Server List DTO
type MCPServerList struct {
	Count int             `json:"count"`
	List  []MCPServerInfo `json:"list"`
}

// MCPServerInfo : MCP Server Info DTO
type MCPServerInfo struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	Description     string   `json:"description"`
	Context         string   `json:"context"`
	Version         string   `json:"version"`
	Provider        string   `json:"provider"`
	Type            string   `json:"type"`
	LifeCycleStatus string   `json:"lifeCycleStatus"`
	WorkflowStatus  string   `json:"workflowStatus"`
	HasThumbnail    bool     `json:"hasThumbnail"`
	SecurityScheme  []string `json:"securityScheme"`
}
