package engine

import (
	"context"
	"fmt"

	wesemail "github.com/weisyn/wesapp/email"
	emailadapter "github.com/weisyn/wesgine/adapter/email"
)

// EmailAccountSummary is the wire type for list responses.
type EmailAccountSummary = emailadapter.AccountSummary

// EmailFilterView is the wire type for email filter settings.
type EmailFilterView struct {
	SkipAutoReply   bool     `json:"skipAutoReply"`
	SenderAllowList []string `json:"senderAllowList"`
	SenderDenyList  []string `json:"senderDenyList"`
}

// EmailAccountDetail is the wire type for detail responses.
type EmailAccountDetail struct {
	AccountID   string          `json:"accountId"`
	SMTPHost    string          `json:"smtpHost"`
	SMTPPort    int             `json:"smtpPort"`
	TLSMode     string          `json:"tlsMode"`
	Username    string          `json:"username"`
	HasPassword bool            `json:"hasPassword"`
	FromName    string          `json:"fromName"`
	FromAddress string          `json:"fromAddress"`
	Enabled     bool            `json:"enabled"`
	IMAPHost    string          `json:"imapHost"`
	IMAPPort    int             `json:"imapPort"`
	IMAPTls     string          `json:"imapTls"`
	Mailbox     string          `json:"mailbox"`
	PollSeconds int             `json:"pollSeconds"`
	IMAPEnabled bool            `json:"imapEnabled"`
	Filter      EmailFilterView `json:"filter"`
}

func configToDetail(accountID string, cfg emailadapter.Config) EmailAccountDetail {
	return EmailAccountDetail{
		AccountID:   accountID,
		SMTPHost:    cfg.SMTPHost,
		SMTPPort:    cfg.SMTPPort,
		TLSMode:     cfg.TLSMode,
		Username:    cfg.Username,
		HasPassword: cfg.Password != "",
		FromName:    cfg.FromName,
		FromAddress: cfg.FromAddress,
		Enabled:     cfg.Enabled,
		IMAPHost:    cfg.IMAPHost,
		IMAPPort:    cfg.IMAPPort,
		IMAPTls:     cfg.IMAPTls,
		Mailbox:     cfg.Mailbox,
		PollSeconds: cfg.PollSeconds,
		IMAPEnabled: cfg.IMAPHost != "",
		Filter: EmailFilterView{
			SkipAutoReply:   cfg.Filter.SkipAutoReply,
			SenderAllowList: cfg.Filter.SenderAllowList,
			SenderDenyList:  cfg.Filter.SenderDenyList,
		},
	}
}

// ─── Email service methods ───────────────────────────────────────────────────

func (s *Service) emailStore() *emailadapter.Store {
	if s.cell == nil {
		return nil
	}
	return s.cell.Email().Store()
}

func (s *Service) ListEmailAccounts(ctx context.Context) ([]EmailAccountSummary, error) {
	store := s.emailStore()
	if store == nil {
		return nil, nil
	}
	return store.ListAccounts(ctx)
}

func (s *Service) GetEmailAccount(ctx context.Context, accountID string) (*EmailAccountDetail, error) {
	store := s.emailStore()
	if store == nil {
		return nil, fmt.Errorf("email store not initialized")
	}
	cfg, err := store.GetAccount(ctx, accountID)
	if err != nil {
		return nil, err
	}
	detail := configToDetail(accountID, cfg)
	return &detail, nil
}

type EmailAccountWrite struct {
	AccountID   string           `json:"accountId"`
	SMTPHost    string           `json:"smtpHost"`
	SMTPPort    int              `json:"smtpPort"`
	TLSMode     string           `json:"tlsMode"`
	Username    string           `json:"username"`
	Password    string           `json:"password"`
	FromName    string           `json:"fromName"`
	FromAddress string           `json:"fromAddress"`
	Enabled     bool             `json:"enabled"`
	IMAPHost    string           `json:"imapHost,omitempty"`
	IMAPPort    int              `json:"imapPort,omitempty"`
	IMAPTls     string           `json:"imapTls,omitempty"`
	Mailbox     string           `json:"mailbox,omitempty"`
	PollSeconds int              `json:"pollSeconds,omitempty"`
	Filter      *EmailFilterView `json:"filter,omitempty"`
}

func (s *Service) SaveEmailAccount(ctx context.Context, accountID string, w EmailAccountWrite) (*EmailAccountDetail, error) {
	store := s.emailStore()
	if store == nil {
		return nil, fmt.Errorf("email store not initialized")
	}

	existing, _ := store.GetAccount(ctx, accountID)
	password := w.Password
	if password == "" && existing.Password != "" {
		password = existing.Password
	}

	cfg := emailadapter.Config{
		SMTPHost:    w.SMTPHost,
		SMTPPort:    w.SMTPPort,
		TLSMode:     w.TLSMode,
		Username:    w.Username,
		Password:    password,
		FromName:    w.FromName,
		FromAddress: w.FromAddress,
		Enabled:     w.Enabled,
		IMAPHost:    w.IMAPHost,
		IMAPPort:    w.IMAPPort,
		IMAPTls:     w.IMAPTls,
		Mailbox:     w.Mailbox,
		PollSeconds: w.PollSeconds,
	}
	if w.Filter != nil {
		cfg.Filter = emailadapter.EmailFilter{
			SkipAutoReply:   w.Filter.SkipAutoReply,
			SenderAllowList: w.Filter.SenderAllowList,
			SenderDenyList:  w.Filter.SenderDenyList,
		}
	}

	if err := store.PutAccount(ctx, accountID, cfg); err != nil {
		return nil, err
	}

	s.syncEmailAccountChannel(ctx, accountID, cfg)

	detail := configToDetail(accountID, cfg)
	return &detail, nil
}

func (s *Service) DeleteEmailAccount(ctx context.Context, accountID string) error {
	store := s.emailStore()
	if store == nil {
		return fmt.Errorf("email store not initialized")
	}
	return store.DeleteAccount(ctx, accountID)
}

func (s *Service) syncEmailAccountChannel(ctx context.Context, accountID string, cfg emailadapter.Config) {
	if s.cell == nil {
		return
	}
	_ = wesemail.SyncChannel(ctx, s.cell, accountID, cfg)
}

func (s *Service) TestEmailAccount(ctx context.Context, accountID string) (string, error) {
	store := s.emailStore()
	if store == nil {
		return "", fmt.Errorf("email store not initialized")
	}
	cfg, err := store.GetAccount(ctx, accountID)
	if err != nil {
		return "", err
	}
	if !cfg.Enabled {
		return "", fmt.Errorf("email account is disabled")
	}
	if err := emailadapter.Send(cfg, cfg.FromAddress, "SMTP Test", "SMTP configuration is working.", false, nil); err != nil {
		return "", err
	}
	return cfg.FromAddress, nil
}
