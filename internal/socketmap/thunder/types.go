package thunder

import (
	"time"
)

// Auth holds Thunder authentication state
type Auth struct {
	BearerToken string
	ExpiresAt   time.Time
	LastRefresh time.Time
}

// UsersResponse represents the response from Thunder Users API
type UsersResponse struct {
	TotalResults int           `json:"totalResults"`
	StartIndex   int           `json:"startIndex"`
	Count        int           `json:"count"`
	Users        []User        `json:"users"`
	Links        []interface{} `json:"links"`
}

// User represents a Thunder user
type User struct {
	ID               string                 `json:"id"`
	OrganizationUnit string                 `json:"ouId"`
	Type             string                 `json:"type"`
	Attributes       map[string]interface{} `json:"attributes"`
}

// GroupsResponse represents the response from Thunder Groups API
type GroupsResponse struct {
	TotalResults int           `json:"totalResults"`
	StartIndex   int           `json:"startIndex"`
	Count        int           `json:"count"`
	Groups       []Group       `json:"groups"`
	Links        []interface{} `json:"links"`
}

// Group represents a Thunder group
type Group struct {
	ID                 string `json:"id"`
	Name               string `json:"name"`
	OrganizationUnitID string `json:"ouId"`
}

// RolesResponse represents the response from Thunder Roles API.
type RolesResponse struct {
	TotalResults int           `json:"totalResults"`
	StartIndex   int           `json:"startIndex"`
	Count        int           `json:"count"`
	Roles        []Role        `json:"roles"`
	Links        []interface{} `json:"links"`
}

// Role represents a Thunder role.
type Role struct {
	ID                 string `json:"id"`
	Name               string `json:"name"`
	OrganizationUnitID string `json:"ouId"`
}
