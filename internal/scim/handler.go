package scim

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"

	protocol "github.com/elimity-com/scim"
	protocolErrors "github.com/elimity-com/scim/errors"
	"github.com/elimity-com/scim/optional"
	"github.com/elimity-com/scim/schema"

	"github.com/Busnes-app/kydrive-server/internal/config"
	"github.com/Busnes-app/kydrive-server/internal/crypto"
	"github.com/Busnes-app/kydrive-server/internal/store"
)

type Server struct {
	config   config.SCIMConfig
	protocol http.Handler
}

func NewServer(st store.Store, cfg config.SCIMConfig, appURL string) *Server {
	userHandler := &userResourceHandler{store: st}
	groupHandler := &groupResourceHandler{store: st}
	server, err := protocol.NewServer(&protocol.ServerArgs{
		ServiceProviderConfig: &protocol.ServiceProviderConfig{
			DocumentationURI: optional.NewString("https://busnes.app/docs/scim"),
			SupportPatch:     true, SupportFiltering: true, MaxResults: 200,
			AuthenticationSchemes: []protocol.AuthenticationScheme{{Type: protocol.AuthenticationTypeOauthBearerToken, Name: "OAuth Bearer Token", Description: "RFC 6750 bearer token", SpecURI: optional.NewString("https://www.rfc-editor.org/rfc/rfc6750"), Primary: true}},
		},
		ResourceTypes: []protocol.ResourceType{
			{ID: optional.NewString("User"), Name: "User", Endpoint: "/Users", Description: optional.NewString("User Account"), Schema: schema.CoreUserSchema(), Handler: userHandler},
			{ID: optional.NewString("Group"), Name: "Group", Endpoint: "/Groups", Description: optional.NewString("Group Resource"), Schema: schema.CoreGroupSchema(), Handler: groupHandler},
		},
	}, protocol.WithBaseURL(strings.TrimRight(appURL, "/")+"/scim/v2"))
	if err != nil {
		return &Server{config: cfg, protocol: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "SCIM initialization failed", http.StatusInternalServerError)
		})}
	}
	return &Server{config: cfg, protocol: server}
}

func (s *Server) RegisterRoutes(mux *http.ServeMux) {
	h := http.StripPrefix("/scim", s.protocol)
	mux.Handle("/scim/v2", h)
	mux.Handle("/scim/v2/", h)
}

func (s *Server) AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/scim/v2") {
			next.ServeHTTP(w, r)
			return
		}
		if !s.config.Enabled {
			writeAuthError(w, http.StatusForbidden, "SCIM provisioning is disabled")
			return
		}
		parts := strings.Fields(r.Header.Get("Authorization"))
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || !constantTimeEqual(parts[1], s.config.BearerToken) {
			w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`)
			writeAuthError(w, http.StatusUnauthorized, "Invalid or missing bearer token")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func constantTimeEqual(a, b string) bool {
	return len(a) == len(b) && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func writeAuthError(w http.ResponseWriter, status int, detail string) {
	w.Header().Set("Content-Type", "application/scim+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(protocolErrors.ScimError{Status: status, Detail: detail})
}

type userResourceHandler struct{ store store.Store }

func (h *userResourceHandler) Create(r *http.Request, attrs protocol.ResourceAttributes) (protocol.Resource, error) {
	username, _ := attrs["userName"].(string)
	user := &store.User{ID: "usr_" + crypto.RandomHex(12), Username: username, Email: primaryValue(attrs["emails"]), DisplayName: stringValue(attrs, "displayName", username), Role: "user", Status: statusFromActive(attrs), SSOProvider: "scim", SSOSubject: stringValue(attrs, "externalId", "")}

	if err := h.store.Users().CreateUser(r.Context(), user); err != nil {
		return protocol.Resource{}, scimStoreError(err, user.ID)
	}
	_ = h.store.Audit().LogAudit(r.Context(), &store.AuditRecord{UserID: user.ID, Action: "scim.user.create", Resource: user.Username})
	return userResource(user), nil
}

func (h *userResourceHandler) Get(r *http.Request, id string) (protocol.Resource, error) {
	user, err := h.directoryUser(r, id)
	if err != nil {
		return protocol.Resource{}, scimStoreError(err, id)
	}
	return userResource(user), nil
}

var equalityFilter = regexp.MustCompile(`(?i)^\s*(userName|externalId|displayName)\s+eq\s+("(?:[^"\\]|\\.)*")\s*$`)

func (h *userResourceHandler) GetAll(r *http.Request, params protocol.ListRequestParams) (protocol.Page, error) {
	search := ""
	if raw := r.URL.Query().Get("filter"); raw != "" {
		field, value, err := filterEquality(raw)
		if err != nil {
			return protocol.Page{}, err
		}
		var user *store.User
		switch field {
		case "externalid":
			user, err = h.store.Users().GetUserBySSO(r.Context(), "scim", value)
		case "username":
			user, err = h.store.Users().GetUserByUsername(r.Context(), value)
		default:
			return protocol.Page{}, protocolErrors.ScimErrorInvalidFilter
		}
		if errors.Is(err, store.ErrNotFound) {
			return protocol.Page{Resources: []protocol.Resource{}}, nil
		}
		if err != nil {
			return protocol.Page{}, err
		}
		return protocol.Page{TotalResults: 1, Resources: []protocol.Resource{userResource(user)}}, nil
	}
	users, total, err := h.store.Users().ListUsers(r.Context(), params.StartIndex-1, params.Count, search)
	if err != nil {
		return protocol.Page{}, err
	}
	resources := make([]protocol.Resource, 0, len(users))
	for _, user := range users {
		resources = append(resources, userResource(user))
	}
	return protocol.Page{TotalResults: total, Resources: resources}, nil
}

func (h *userResourceHandler) Replace(r *http.Request, id string, attrs protocol.ResourceAttributes) (protocol.Resource, error) {
	user, err := h.directoryUser(r, id)
	if err != nil {
		return protocol.Resource{}, scimStoreError(err, id)
	}
	oldRole, oldStatus := user.Role, user.Status
	user.Username, _ = attrs["userName"].(string)
	user.Email = primaryValue(attrs["emails"])
	user.DisplayName = stringValue(attrs, "displayName", user.Username)

	user.Status = statusFromActive(attrs)
	if err := h.store.Users().UpdateUser(r.Context(), user); err != nil {
		return protocol.Resource{}, scimStoreError(err, id)
	}
	h.revokeIfPrivilegesChanged(r, user, oldRole, oldStatus)
	return userResource(user), nil
}

func (h *userResourceHandler) Delete(r *http.Request, id string) error {
	if _, err := h.directoryUser(r, id); err != nil {
		return err
	}
	return scimStoreError(h.store.Users().DeleteUser(r.Context(), id), id)
}

func (h *userResourceHandler) Patch(r *http.Request, id string, operations []protocol.PatchOperation) (protocol.Resource, error) {
	user, err := h.directoryUser(r, id)
	if err != nil {
		return protocol.Resource{}, scimStoreError(err, id)
	}
	oldRole, oldStatus := user.Role, user.Status
	for _, op := range operations {
		if op.Op == protocol.PatchOperationRemove {
			continue
		}
		if op.Path == nil {
			if values, ok := op.Value.(map[string]interface{}); ok {
				applyUserValues(user, values)
			}
			continue
		}
		applyUserValue(user, strings.ToLower(op.Path.String()), op.Value)
	}
	if err := h.store.Users().UpdateUser(r.Context(), user); err != nil {
		return protocol.Resource{}, scimStoreError(err, id)
	}
	h.revokeIfPrivilegesChanged(r, user, oldRole, oldStatus)
	return userResource(user), nil
}

func (h *userResourceHandler) revokeIfPrivilegesChanged(r *http.Request, user *store.User, oldRole, oldStatus string) {
	if user.Role != oldRole || user.Status != oldStatus {
		_ = h.store.Sessions().DeleteUserSessions(r.Context(), user.ID)
	}
}

func applyUserValues(user *store.User, values map[string]interface{}) {
	for key, value := range values {
		applyUserValue(user, strings.ToLower(key), value)
	}
}
func applyUserValue(user *store.User, path string, value interface{}) {
	switch path {
	case "active":
		if active, ok := value.(bool); ok {
			if active {
				user.Status = "active"
			} else {
				user.Status = "inactive"
			}
		}
	case "displayname":
		if v, ok := value.(string); ok {
			user.DisplayName = v
		}
	case "username":
		if v, ok := value.(string); ok {
			user.Username = v
		}
	case "roles", "role":
	// Application administration is not a directory role.
	case "emails":
		user.Email = primaryValue(value)
	}
}

func userResource(user *store.User) protocol.Resource {
	attrs := protocol.ResourceAttributes{"userName": user.Username, "displayName": user.DisplayName, "active": user.Status == "active"}
	if user.Email != "" {
		attrs["emails"] = []interface{}{map[string]interface{}{"value": user.Email, "type": "work", "primary": true}}
	}
	if user.Role != "" {
		attrs["roles"] = []interface{}{map[string]interface{}{"value": user.Role, "primary": true}}
	}
	return protocol.Resource{ID: user.ID, ExternalID: optional.NewString(user.SSOSubject), Attributes: attrs, Meta: protocol.Meta{Created: &user.CreatedAt, LastModified: &user.UpdatedAt}}
}

type groupResourceHandler struct{ store store.Store }

func (h *groupResourceHandler) Create(r *http.Request, attrs protocol.ResourceAttributes) (protocol.Resource, error) {
	group := &store.Group{Members: memberValues(attrs["members"]), ID: "grp_" + crypto.RandomHex(12), DisplayName: stringValue(attrs, "displayName", ""), ExternalID: stringValue(attrs, "externalId", "")}
	if err := h.store.Groups().ReplaceGroup(r.Context(), group, true); err != nil {
		return protocol.Resource{}, scimStoreError(err, group.ID)
	}

	group.Members = memberValues(attrs["members"])
	return groupResource(group), nil
}
func (h *groupResourceHandler) Get(r *http.Request, id string) (protocol.Resource, error) {
	group, err := h.store.Groups().GetGroupByID(r.Context(), id)
	if err != nil {
		return protocol.Resource{}, scimStoreError(err, id)
	}
	return groupResource(group), nil
}
func (h *groupResourceHandler) GetAll(r *http.Request, params protocol.ListRequestParams) (protocol.Page, error) {
	if raw := r.URL.Query().Get("filter"); raw != "" {
		field, value, err := filterEquality(raw)
		if err != nil {
			return protocol.Page{}, err
		}
		var group *store.Group
		switch field {
		case "externalid":
			group, err = h.store.Groups().GetGroupByExternalID(r.Context(), value)
		case "displayname":
			group, err = h.store.Groups().GetGroupByName(r.Context(), value)
		default:
			return protocol.Page{}, protocolErrors.ScimErrorInvalidFilter
		}
		if errors.Is(err, store.ErrNotFound) {
			return protocol.Page{Resources: []protocol.Resource{}}, nil
		}
		if err != nil {
			return protocol.Page{}, err
		}
		return protocol.Page{TotalResults: 1, Resources: []protocol.Resource{groupResource(group)}}, nil
	}
	groups, total, err := h.store.Groups().ListGroups(r.Context(), params.StartIndex-1, params.Count)
	if err != nil {
		return protocol.Page{}, err
	}
	resources := make([]protocol.Resource, 0, len(groups))
	for _, group := range groups {
		resources = append(resources, groupResource(group))
	}
	return protocol.Page{TotalResults: total, Resources: resources}, nil
}
func (h *groupResourceHandler) Replace(r *http.Request, id string, attrs protocol.ResourceAttributes) (protocol.Resource, error) {
	group, err := h.store.Groups().GetGroupByID(r.Context(), id)
	if err != nil {
		return protocol.Resource{}, scimStoreError(err, id)
	}
	group.DisplayName = stringValue(attrs, "displayName", group.DisplayName)
	if value := stringValue(attrs, "externalId", group.ExternalID); value != group.ExternalID {
		return protocol.Resource{}, protocolErrors.ScimErrorInvalidValue
	}
	group.Members = memberValues(attrs["members"])
	if err := h.store.Groups().ReplaceGroup(r.Context(), group, false); err != nil {
		return protocol.Resource{}, scimStoreError(err, id)
	}

	return groupResource(group), nil
}
func (h *groupResourceHandler) Delete(r *http.Request, id string) error {
	return scimStoreError(h.store.Groups().DeleteGroup(r.Context(), id), id)
}
func (h *groupResourceHandler) Patch(r *http.Request, id string, operations []protocol.PatchOperation) (protocol.Resource, error) {
	group, err := h.store.Groups().GetGroupByID(r.Context(), id)
	if err != nil {
		return protocol.Resource{}, scimStoreError(err, id)
	}
	for _, op := range operations {
		if op.Path == nil {
			values, ok := op.Value.(map[string]interface{})
			if !ok {
				return protocol.Resource{}, protocolErrors.ScimErrorInvalidValue
			}
			for key, val := range values {
				switch strings.ToLower(key) {
				case "displayname":
					name, ok := val.(string)
					if !ok {
						return protocol.Resource{}, protocolErrors.ScimErrorInvalidValue
					}
					group.DisplayName = name
				case "members":
					if op.Op == protocol.PatchOperationAdd {
						group.Members = append(group.Members, memberValues(val)...)
					} else {
						group.Members = memberValues(val)
					}
				default:
					return protocol.Resource{}, protocolErrors.ScimErrorInvalidPath
				}
			}
			continue
		}
		path := strings.ToLower(op.Path.String())
		switch path {
		case "displayname":
			value, ok := op.Value.(string)
			if !ok || op.Op == protocol.PatchOperationRemove {
				return protocol.Resource{}, protocolErrors.ScimErrorInvalidValue
			}
			group.DisplayName = value
		case "members":
			switch op.Op {
			case protocol.PatchOperationAdd:
				group.Members = append(group.Members, memberValues(op.Value)...)
			case protocol.PatchOperationRemove:
				group.Members = nil
			case protocol.PatchOperationReplace:
				group.Members = memberValues(op.Value)
			}
		default:
			match := memberRemoveFilter.FindStringSubmatch(op.Path.String())
			if len(match) != 2 || op.Op != protocol.PatchOperationRemove {
				return protocol.Resource{}, protocolErrors.ScimErrorInvalidPath
			}
			var id string
			if json.Unmarshal([]byte(match[1]), &id) != nil {
				return protocol.Resource{}, protocolErrors.ScimErrorInvalidValue
			}
			members := []string{}
			for _, m := range group.Members {
				if m != id {
					members = append(members, m)
				}
			}
			group.Members = members
		}
	}
	if err = h.store.Groups().ReplaceGroup(r.Context(), group, false); err != nil {
		return protocol.Resource{}, scimStoreError(err, id)
	}
	return groupResource(group), nil
}

var memberRemoveFilter = regexp.MustCompile(`(?i)^members\[value\s+eq\s+("(?:[^"\\]|\\.)*")\]$`)

func groupResource(group *store.Group) protocol.Resource {
	return protocol.Resource{ID: group.ID, ExternalID: optional.NewString(group.ExternalID), Attributes: protocol.ResourceAttributes{"displayName": group.DisplayName, "members": memberMaps(group.Members)}, Meta: protocol.Meta{Created: &group.CreatedAt, LastModified: &group.UpdatedAt}}
}
func memberMaps(ids []string) []interface{} {
	out := make([]interface{}, 0, len(ids))
	for _, id := range ids {
		out = append(out, map[string]interface{}{"value": id})
	}
	return out
}
func memberValues(value interface{}) []string {
	var out []string
	for _, item := range interfaceSlice(value) {
		if m, ok := item.(map[string]interface{}); ok {
			if v, ok := m["value"].(string); ok && v != "" {
				out = append(out, v)
			}
		}
	}
	return out
}
func primaryValue(value interface{}) string {
	for _, item := range interfaceSlice(value) {
		if m, ok := item.(map[string]interface{}); ok {
			if v, ok := m["value"].(string); ok {
				return v
			}
		}
		if v, ok := item.(string); ok {
			return v
		}
	}
	if v, ok := value.(string); ok {
		return v
	}
	return ""
}
func interfaceSlice(value interface{}) []interface{} {
	if values, ok := value.([]interface{}); ok {
		return values
	}
	return nil
}
func stringValue(attrs protocol.ResourceAttributes, key, fallback string) string {
	if value, ok := attrs[key].(string); ok && value != "" {
		return value
	}
	return fallback
}
func statusFromActive(attrs protocol.ResourceAttributes) string {
	if active, ok := attrs["active"].(bool); ok && !active {
		return "inactive"
	}
	return "active"
}
func scimStoreError(err error, id string) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, store.ErrNotFound) {
		return protocolErrors.ScimErrorResourceNotFound(id)
	}
	if errors.Is(err, store.ErrAlreadyExists) {
		return protocolErrors.ScimErrorUniqueness
	}
	return err
}

func filterEquality(raw string) (string, string, error) {
	match := equalityFilter.FindStringSubmatch(raw)
	if len(match) != 3 {
		return "", "", protocolErrors.ScimErrorInvalidFilter
	}
	var value string
	if json.Unmarshal([]byte(match[2]), &value) != nil {
		return "", "", protocolErrors.ScimErrorInvalidFilter
	}
	return strings.ToLower(match[1]), value, nil
}

func (h *userResourceHandler) directoryUser(r *http.Request, id string) (*store.User, error) {
	user, err := h.store.Users().GetUserByID(r.Context(), id)
	if err != nil {
		return nil, err
	}
	if user.SSOProvider != "scim" {
		return nil, store.ErrNotFound
	}
	return user, nil
}
