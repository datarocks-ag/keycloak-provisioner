package provisioner

import (
	"context"
	"fmt"
	"log/slog"

	"keycloak-provisioner/internal/config"
)

// defaultFlowProviderId is the flow type Keycloak assumes when none is given.
const defaultFlowProviderId = "basic-flow"

// ensureAuthenticationFlow creates a top-level authentication flow and its
// execution tree.
//
// Flows are create-only. When the alias already exists the flow is left exactly
// as it is, whatever the strategy, and the skip is logged at INFO so a config
// edit that does not take effect is visible. Reconciling an existing flow would
// require diffing an ordered tree whose entries have no stable name, and
// deleting the executions that are not configured; the provisioner does not
// remove anything anywhere else and does not start here.
//
// Because Keycloak appends each execution as it is added, creating a flow in
// declared order yields the declared order with no reordering calls.
func (p *Provisioner) ensureAuthenticationFlow(ctx context.Context, realm string, f config.AuthenticationFlow) error {
	flows, err := p.client.GetAuthenticationFlows(ctx, realm)
	if err != nil {
		return err
	}

	if existing := findFlowByAlias(flows, f.Alias); existing != nil {
		if builtIn, _ := existing["builtIn"].(bool); builtIn {
			return fmt.Errorf(
				"authentication flow %q is a built-in Keycloak flow and will not be modified; "+
					"declare a new alias with copyFrom: %q to customise it", f.Alias, f.Alias)
		}

		slog.Info("Skipping existing authentication flow", "realm", realm, "flow", f.Alias)

		return nil
	}

	if f.CopyFrom != "" {
		if findFlowByAlias(flows, f.CopyFrom) == nil {
			// Keycloak validates the source on the copy call itself, and the
			// listing can legitimately be incomplete: in a dry run against a
			// realm that does not exist yet, none of its built-in flows are
			// visible. Warn rather than abort, and let the copy be the
			// authority on whether the source exists.
			slog.Warn("copyFrom source not visible in the current flow listing",
				"realm", realm, "flow", f.Alias, "copyFrom", f.CopyFrom)
		}

		slog.Info("Copying authentication flow", "realm", realm, "flow", f.Alias, "from", f.CopyFrom)

		if err := p.client.CopyAuthenticationFlow(ctx, realm, f.CopyFrom, f.Alias); err != nil {
			return err
		}
	} else {
		slog.Info("Creating authentication flow", "realm", realm, "flow", f.Alias)

		body := map[string]any{
			"alias":      f.Alias,
			"providerId": flowProviderId(f.ProviderId),
			"topLevel":   true,
			"builtIn":    false,
		}
		if f.Description != "" {
			body["description"] = f.Description
		}

		if err := p.client.CreateAuthenticationFlow(ctx, realm, body); err != nil {
			return err
		}
	}

	return p.createFlowExecutions(ctx, realm, f.Alias, f.Executions)
}

// createFlowExecutions appends each declared execution to flowAlias in order,
// then sets its requirement and configuration. Subflows recurse under their own
// alias.
func (p *Provisioner) createFlowExecutions(ctx context.Context, realm, flowAlias string, executions []config.AuthenticationExecution) error {
	for _, e := range executions {
		if e.Subflow != "" {
			if err := p.createSubflow(ctx, realm, flowAlias, e); err != nil {
				return err
			}
			continue
		}

		slog.Info("Adding authentication execution", "realm", realm, "flow", flowAlias, "provider", e.Provider)

		if err := p.client.CreateAuthenticationExecution(ctx, realm, flowAlias, map[string]any{"provider": e.Provider}); err != nil {
			return err
		}

		if err := p.finalizeExecution(ctx, realm, flowAlias, e, executionMatchesProvider(e.Provider)); err != nil {
			return err
		}
	}

	return nil
}

func (p *Provisioner) createSubflow(ctx context.Context, realm, flowAlias string, e config.AuthenticationExecution) error {
	slog.Info("Adding authentication subflow", "realm", realm, "flow", flowAlias, "subflow", e.Subflow)

	body := map[string]any{
		"alias": e.Subflow,
		"type":  flowProviderId(e.ProviderId),
	}
	if e.Description != "" {
		body["description"] = e.Description
	}

	if err := p.client.CreateAuthenticationSubflow(ctx, realm, flowAlias, body); err != nil {
		return err
	}

	if err := p.finalizeExecution(ctx, realm, flowAlias, e, executionMatchesSubflow(e.Subflow)); err != nil {
		return err
	}

	return p.createFlowExecutions(ctx, realm, e.Subflow, e.Executions)
}

// finalizeExecution sets the requirement and configuration of the execution
// that was just added. Keycloak assigns the new execution the authenticator's
// default requirement and returns no id from the create call, so the flow's
// executions are read back and the entry located with match.
func (p *Provisioner) finalizeExecution(
	ctx context.Context,
	realm, flowAlias string,
	e config.AuthenticationExecution,
	match func(map[string]any) bool,
) error {
	if e.Requirement == "" && len(e.Config) == 0 {
		return nil
	}

	executions, err := p.client.GetAuthenticationFlowExecutions(ctx, realm, flowAlias)
	if err != nil {
		return err
	}

	// The execution was just appended, so the last match is the one added here
	// even when the same provider appears more than once in the flow.
	var found map[string]any
	for _, ex := range executions {
		if match(ex) {
			found = ex
		}
	}
	if found == nil {
		// Dry-run reads return nothing for a flow that would only have been
		// created, so there is nothing to finalise.
		return nil
	}

	if e.Requirement != "" {
		slog.Info("Setting execution requirement", "realm", realm, "flow", flowAlias, "execution", executionLabel(e), "requirement", e.Requirement)

		body := map[string]any{}
		for k, v := range found {
			body[k] = v
		}
		body["requirement"] = e.Requirement

		if err := p.client.UpdateAuthenticationFlowExecution(ctx, realm, flowAlias, body); err != nil {
			return err
		}
	}

	if len(e.Config) == 0 {
		return nil
	}

	executionID, ok := found["id"].(string)
	if !ok {
		return fmt.Errorf("authentication execution %q: missing or invalid id in response", executionLabel(e))
	}

	return p.createExecutionConfig(ctx, realm, flowAlias, executionID, e)
}

func (p *Provisioner) createExecutionConfig(ctx context.Context, realm, flowAlias, executionID string, e config.AuthenticationExecution) error {
	cfg := make(map[string]string, len(e.Config))
	for k, v := range e.Config {
		cfg[k] = v
	}

	// "alias" names the config itself rather than being a config entry.
	alias := cfg["alias"]
	delete(cfg, "alias")
	if alias == "" {
		alias = flowAlias + "-" + executionLabel(e)
	}

	slog.Info("Setting execution config", "realm", realm, "flow", flowAlias, "execution", executionLabel(e), "configAlias", alias)

	return p.client.CreateAuthenticationExecutionConfig(ctx, realm, executionID, map[string]any{
		"alias":  alias,
		"config": cfg,
	})
}

// ensureAuthenticationBindings points the realm's flow bindings at the given
// aliases. It is a second realm update, because the flows have to exist first.
func (p *Provisioner) ensureAuthenticationBindings(ctx context.Context, realm string, b *config.AuthenticationBindings) error {
	if b == nil {
		return nil
	}

	body := map[string]any{"realm": realm}

	for key, alias := range map[string]string{
		"browserFlow":              b.BrowserFlow,
		"directGrantFlow":          b.DirectGrantFlow,
		"resetCredentialsFlow":     b.ResetCredentialsFlow,
		"registrationFlow":         b.RegistrationFlow,
		"clientAuthenticationFlow": b.ClientAuthenticationFlow,
		"dockerAuthenticationFlow": b.DockerAuthenticationFlow,
		"firstBrokerLoginFlow":     b.FirstBrokerLoginFlow,
	} {
		if alias != "" {
			body[key] = alias
		}
	}

	if len(body) == 1 {
		return nil
	}

	slog.Info("Updating realm authentication bindings", "realm", realm)

	return p.client.UpdateRealm(ctx, realm, body)
}

// resolveFlowBindingOverrides turns the client's flow aliases into the flow IDs
// Keycloak expects in authenticationFlowBindingOverrides. Realm bindings take
// aliases, but this one takes IDs.
func (p *Provisioner) resolveFlowBindingOverrides(ctx context.Context, realm, clientID string, overrides map[string]string) (map[string]any, error) {
	if len(overrides) == 0 {
		return nil, nil
	}

	flows, err := p.client.GetAuthenticationFlows(ctx, realm)
	if err != nil {
		return nil, err
	}

	resolved := make(map[string]any, len(overrides))

	for binding, alias := range overrides {
		flow := findFlowByAlias(flows, alias)
		if flow == nil {
			return nil, fmt.Errorf("client %q: authentication flow %q not found in realm %q", clientID, alias, realm)
		}

		id, ok := flow["id"].(string)
		if !ok {
			return nil, fmt.Errorf("authentication flow %q: missing or invalid id in response", alias)
		}
		resolved[binding] = id
	}

	return resolved, nil
}

func findFlowByAlias(flows []map[string]any, alias string) map[string]any {
	for _, f := range flows {
		if a, ok := f["alias"].(string); ok && a == alias {
			return f
		}
	}
	return nil
}

func flowProviderId(providerId string) string {
	if providerId == "" {
		return defaultFlowProviderId
	}
	return providerId
}

func executionMatchesProvider(provider string) func(map[string]any) bool {
	return func(ex map[string]any) bool {
		if isFlow, _ := ex["authenticationFlow"].(bool); isFlow {
			return false
		}
		p, _ := ex["providerId"].(string)
		return p == provider
	}
}

func executionMatchesSubflow(alias string) func(map[string]any) bool {
	return func(ex map[string]any) bool {
		if isFlow, _ := ex["authenticationFlow"].(bool); !isFlow {
			return false
		}
		a, _ := ex["displayName"].(string)
		return a == alias
	}
}

func executionLabel(e config.AuthenticationExecution) string {
	if e.Subflow != "" {
		return e.Subflow
	}
	return e.Provider
}
