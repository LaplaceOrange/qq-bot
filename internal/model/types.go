package model

import "time"

type AuditRecord struct {
	At          time.Time      `json:"at"`
	Actor       string         `json:"actor"`
	Action      string         `json:"action"`
	Target      string         `json:"target,omitempty"`
	Success     bool           `json:"success"`
	Description string         `json:"description,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
}

type PendingGatewayEvent struct {
	Key       string    `json:"-"`
	Payload   []byte    `json:"payload"`
	CreatedAt time.Time `json:"created_at"`
}

type QQIdentity struct {
	UnionOpenID  string
	UserOpenID   string
	MemberOpenID string
	GroupOpenID  string
}

func (i QQIdentity) Canonical() string {
	if i.UnionOpenID != "" {
		return "union:" + i.UnionOpenID
	}
	if i.UserOpenID != "" {
		return "user:" + i.UserOpenID
	}
	return ""
}

func (i QQIdentity) GroupAlias() string {
	if i.GroupOpenID != "" && i.MemberOpenID != "" {
		return "member:" + i.GroupOpenID + ":" + i.MemberOpenID
	}
	return ""
}

func (i QQIdentity) AdminCandidates() []string {
	result := make([]string, 0, 3)
	if i.UnionOpenID != "" {
		result = append(result, "union:"+i.UnionOpenID)
	}
	if i.UserOpenID != "" {
		result = append(result, "user:"+i.UserOpenID)
	}
	if alias := i.GroupAlias(); alias != "" {
		result = append(result, alias)
	}
	return result
}
