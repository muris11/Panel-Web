// Package collector fills the tables the dashboard reads: traffic per account
// and website, CPU and memory per account, and certificate expiry.
//
// It replaces the scheduled Laravel commands (CollectBandwidthCommand,
// CollectResourcesCommand, SyncSslExpiryCommand) with a goroutine inside the
// panel, so a host running this panel needs no queue worker, no scheduler
// timer and no PHP to keep its numbers current. `muriscore-panel collect` runs
// the same jobs once, for hosts that would rather drive them from cron.
package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"muriscore/panel/internal/store"
)

// Agent is the part of the agent client the collectors use.
type Agent interface {
	Call(ctx context.Context, method string, params any) (json.RawMessage, error)
}

// Collector runs the jobs against one store.
type Collector struct {
	store  *store.Store
	agent  Agent
	logger *slog.Logger
	// Now is replaced in tests so a day's bucket is predictable.
	Now func() time.Time
}

// New returns a collector that writes through st and reads through client.
func New(st *store.Store, client Agent, logger *slog.Logger) *Collector {
	return &Collector{store: st, agent: client, logger: logger, Now: time.Now}
}

// Job is one unit of work with the name it is recorded under.
type Job struct {
	Name string
	Run  func(ctx context.Context) (string, error)
}

// Jobs lists them in the order a run performs them.
func (c *Collector) Jobs() []Job {
	return []Job{
		{"bandwidth", c.Bandwidth},
		{"resources", c.Resources},
		{"ssl_expiry", c.SSLExpiry},
	}
}

// Run performs every job and records the outcome of each. One failing job does
// not stop the others: a missing certificate should not stop traffic being
// counted.
func (c *Collector) Run(ctx context.Context) error {
	var firstErr error

	for _, job := range c.Jobs() {
		started := time.Now()

		detail, err := job.Run(ctx)

		if err != nil {
			c.logger.Warn("collector failed", "job", job.Name, "error", err)

			if firstErr == nil {
				firstErr = fmt.Errorf("%s: %w", job.Name, err)
			}
		}

		if recordErr := c.store.RecordCollectorRun(ctx, job.Name, started, err == nil, detailOr(detail, err)); recordErr != nil {
			c.logger.Warn("could not record a collector run", "job", job.Name, "error", recordErr)
		}
	}

	return firstErr
}

func detailOr(detail string, err error) string {
	if err != nil {
		return err.Error()
	}

	return detail
}

// Bandwidth counts traffic since the last count and adds it to the account and
// website totals for the days it happened. The cursor moves in the same
// transaction as the bytes, so a crash re-reads a log rather than losing it.
func (c *Collector) Bandwidth(ctx context.Context) (string, error) {
	owners, err := c.store.DomainOwners(ctx)
	if err != nil {
		return "", err
	}

	logins, err := c.store.FTPOwners(ctx)
	if err != nil {
		return "", err
	}

	accounts, err := c.store.AccountIDsByName(ctx)
	if err != nil {
		return "", err
	}

	cursor, err := c.store.BandwidthCursor(ctx)
	if err != nil {
		return "", err
	}

	var added uint64

	for count := 0; count < 10; count++ {
		params := map[string]any{"cursor": json.RawMessage(`{}`)}
		if len(cursor) > 0 {
			params["cursor"] = cursor
		}

		raw, err := c.agent.Call(ctx, "usage.bandwidth", params)
		if err != nil {
			return "", err
		}

		var report bandwidthReport
		if err := json.Unmarshal(raw, &report); err != nil {
			return "", fmt.Errorf("the agent's bandwidth report is not readable: %w", err)
		}

		totals, domains := attribute(report, owners, logins, accounts)

		if err := c.store.RecordBandwidth(ctx, totals, domains, report.Cursor); err != nil {
			return "", err
		}

		for _, entry := range totals {
			added += entry.Web + entry.Mail + entry.FTP
		}

		if report.Cursor != nil {
			cursor = report.Cursor
		}

		if report.Complete {
			break
		}
	}

	return fmt.Sprintf("counted %d bytes", added), nil
}

// bandwidthReport is the agent's usage.bandwidth answer.
type bandwidthReport struct {
	Web      []domainBytes   `json:"web"`
	Mail     []domainBytes   `json:"mail"`
	MailUser []userBytes     `json:"mail_user"`
	FTP      []userBytes     `json:"ftp"`
	Cursor   json.RawMessage `json:"cursor"`
	Complete bool            `json:"complete"`
}

type domainBytes struct {
	Domain string `json:"domain"`
	Date   string `json:"date"`
	Bytes  uint64 `json:"bytes"`
}

type userBytes struct {
	User  string `json:"user"`
	Date  string `json:"date"`
	Bytes uint64 `json:"bytes"`
}

// attribute turns one report into per-account rows and per-website rows.
//
// The rules are the ones the Laravel panel used, because the rows are shared:
// web and mail traffic is charged to the account that owns the domain and to
// the website; FTP traffic to the account that owns the login; mail submitted
// locally by a Unix user to the account with that username. Anything the panel
// does not recognise is dropped rather than guessed at.
func attribute(report bandwidthReport, owners map[string]store.DomainOwner, logins, accounts map[string]int64) ([]store.Accounting, map[int64]map[string]uint64) {
	byAccount := map[int64]map[string]*store.Accounting{}
	websites := map[int64]map[string]uint64{}

	row := func(userID int64, date string) *store.Accounting {
		if byAccount[userID] == nil {
			byAccount[userID] = map[string]*store.Accounting{}
		}

		if byAccount[userID][date] == nil {
			byAccount[userID][date] = &store.Accounting{UserID: userID, Date: date}
		}

		return byAccount[userID][date]
	}

	for _, entry := range report.Web {
		owner, ok := owners[strings.ToLower(entry.Domain)]
		if !ok || entry.Bytes == 0 {
			continue
		}

		row(owner.UserID, entry.Date).Web += entry.Bytes

		if websites[owner.ID] == nil {
			websites[owner.ID] = map[string]uint64{}
		}

		websites[owner.ID][entry.Date] += entry.Bytes
	}

	for _, entry := range report.Mail {
		owner, ok := owners[strings.ToLower(entry.Domain)]
		if !ok || entry.Bytes == 0 {
			continue
		}

		row(owner.UserID, entry.Date).Mail += entry.Bytes

		if websites[owner.ID] == nil {
			websites[owner.ID] = map[string]uint64{}
		}

		websites[owner.ID][entry.Date] += entry.Bytes
	}

	for _, entry := range report.FTP {
		userID, ok := logins[entry.User]
		if !ok || entry.Bytes == 0 {
			continue
		}

		row(userID, entry.Date).FTP += entry.Bytes
	}

	for _, entry := range report.MailUser {
		userID, ok := accounts[entry.User]
		if !ok || entry.Bytes == 0 {
			continue
		}

		row(userID, entry.Date).Mail += entry.Bytes
	}

	totals := make([]store.Accounting, 0, len(byAccount))

	for _, days := range byAccount {
		for _, entry := range days {
			totals = append(totals, *entry)
		}
	}

	return totals, websites
}

// Resources measures CPU time and peak memory per account. The agent reports
// each cgroup's total CPU since it was created; the panel keeps the previous
// total and stores the difference, so a restart of a cgroup (a new id) starts
// from zero instead of going backwards.
func (c *Collector) Resources(ctx context.Context) (string, error) {
	accounts, err := c.store.AccountIDsByName(ctx)
	if err != nil {
		return "", err
	}

	previous, err := c.store.ResourceCounters(ctx)
	if err != nil {
		return "", err
	}

	raw, err := c.agent.Call(ctx, "usage.resources", map[string]any{})
	if err != nil {
		return "", err
	}

	var report struct {
		Accounts []struct {
			Username    string `json:"username"`
			MemoryBytes uint64 `json:"memory_bytes"`
			CPU         []struct {
				ID   uint64 `json:"id"`
				Usec uint64 `json:"usec"`
			} `json:"cpu"`
		} `json:"accounts"`
	}

	if err := json.Unmarshal(raw, &report); err != nil {
		return "", fmt.Errorf("the agent's resource report is not readable: %w", err)
	}

	today := c.Now().Format("2006-01-02")

	var usage []store.AccountUsage

	counters := map[int64]store.ResourceCounter{}

	for _, account := range report.Accounts {
		userID, ok := accounts[account.Username]
		if !ok {
			continue
		}

		var total uint64
		var groupID uint64

		for _, group := range account.CPU {
			total += group.Usec

			if group.ID > groupID {
				groupID = group.ID
			}
		}

		delta := total

		if before, ok := previous[userID]; ok && total >= before.Usec {
			delta = total - before.Usec
		}

		counters[userID] = store.ResourceCounter{CgroupID: groupID, Usec: total}

		usage = append(usage, store.AccountUsage{
			UserID:     userID,
			CPUms:      delta / 1000,
			MemoryPeak: int64(account.MemoryBytes / (1 << 20)),
		})
	}

	if err := c.store.RecordResources(ctx, today, usage, counters); err != nil {
		return "", err
	}

	return fmt.Sprintf("measured %d account(s)", len(usage)), nil
}

// SSLExpiry asks the agent when each certificate expires, so renewals certbot
// makes on its own show up in the panel without a task.
func (c *Collector) SSLExpiry(ctx context.Context) (string, error) {
	domains, err := c.store.DomainsForExpiry(ctx)
	if err != nil {
		return "", err
	}

	if len(domains) == 0 {
		return "no websites to check", nil
	}

	names := make([]string, 0, len(domains))
	for _, domain := range domains {
		names = append(names, domain.Name)
	}

	raw, err := c.agent.Call(ctx, "ssl.expiry", map[string]any{"domains": names})
	if err != nil {
		return "", err
	}

	var report struct {
		ExpiresAt map[string]string `json:"expires_at"`
	}

	if err := json.Unmarshal(raw, &report); err != nil {
		return "", fmt.Errorf("the agent's certificate report is not readable: %w", err)
	}

	for _, domain := range domains {
		value, ok := report.ExpiresAt[domain.Name]

		if !ok || value == "" {
			// No certificate on disk: clear a stale date rather than keep
			// showing a renewal that is not there.
			if err := c.store.SetDomainExpiry(ctx, domain.Name, nil); err != nil {
				return "", err
			}

			continue
		}

		expires, err := time.Parse(time.RFC3339, value)
		if err != nil {
			c.logger.Warn("the agent sent a certificate date that does not parse", "domain", domain.Name, "value", value)

			continue
		}

		if err := c.store.SetDomainExpiry(ctx, domain.Name, &expires); err != nil {
			return "", err
		}
	}

	return fmt.Sprintf("checked %d certificate(s)", len(domains)), nil
}
