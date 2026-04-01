package cli

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/eemax/tinyflags/internal/core"
	cerr "github.com/eemax/tinyflags/internal/errors"
	"github.com/eemax/tinyflags/internal/mode"
	"github.com/eemax/tinyflags/internal/provider/openrouter"
)

type modelRequirement struct {
	Name             string   `json:"name"`
	Model            string   `json:"model"`
	RequiredFeatures []string `json:"required_features,omitempty"`
}

type modelCheck struct {
	Name             string   `json:"name"`
	Model            string   `json:"model"`
	RequiredFeatures []string `json:"required_features,omitempty"`
	MissingFeatures  []string `json:"missing_features,omitempty"`
	Error            string   `json:"error,omitempty"`
}

type modelValidationReport struct {
	Checks   []modelCheck `json:"checks,omitempty"`
	Warnings []string     `json:"warnings,omitempty"`
}

type modelCatalogFetcher func(context.Context) (*openrouter.ModelCatalog, error)

func validateConfigModels(ctx context.Context, cfg core.Config, httpClient *http.Client) (modelValidationReport, error) {
	requirements, err := configModelRequirements(cfg)
	if err != nil {
		return modelValidationReport{}, err
	}
	return evaluateModelRequirements(ctx, requirements, func(ctx context.Context) (*openrouter.ModelCatalog, error) {
		return openrouter.FetchModelCatalog(ctx, cfg.BaseURL, cfg.APIKey, httpClient)
	})
}

func validateRunModel(ctx context.Context, client *openrouter.Client, modeCfg core.ResolvedMode, schemaRequired bool) error {
	requirements := []modelRequirement{{
		Name:             "run:" + modeCfg.Name,
		Model:            modeCfg.Model,
		RequiredFeatures: requiredModelFeatures(modeCfg, schemaRequired),
	}}
	report, err := evaluateModelRequirements(ctx, requirements, client.FetchModelCatalog)
	if err != nil {
		return err
	}
	if !report.HasFailures() {
		return nil
	}
	return cerr.New(cerr.ExitRuntime, strings.Join(report.FailureMessages(), "; "))
}

func evaluateModelRequirements(ctx context.Context, requirements []modelRequirement, fetchCatalog modelCatalogFetcher) (modelValidationReport, error) {
	catalog, err := fetchCatalog(ctx)
	if err != nil {
		return modelValidationReport{
			Warnings: []string{fmt.Sprintf("unable to fetch OpenRouter model catalog: %v", err)},
		}, nil
	}

	report := modelValidationReport{Checks: make([]modelCheck, 0, len(requirements))}
	for _, requirement := range requirements {
		check := modelCheck{
			Name:             requirement.Name,
			Model:            requirement.Model,
			RequiredFeatures: append([]string(nil), requirement.RequiredFeatures...),
		}
		info, ok := catalog.Lookup(requirement.Model)
		if !ok {
			check.Error = fmt.Sprintf("model %q was not found in the OpenRouter catalog", requirement.Model)
			report.Checks = append(report.Checks, check)
			continue
		}
		for _, feature := range requirement.RequiredFeatures {
			if !info.SupportsParameter(feature) {
				check.MissingFeatures = append(check.MissingFeatures, feature)
			}
		}
		if len(check.MissingFeatures) > 0 {
			check.Error = fmt.Sprintf("model %q does not support required features: %s", requirement.Model, strings.Join(check.MissingFeatures, ", "))
		}
		report.Checks = append(report.Checks, check)
	}
	return report, nil
}

func configModelRequirements(cfg core.Config) ([]modelRequirement, error) {
	requirements := []modelRequirement{}
	if strings.TrimSpace(cfg.DefaultModel) != "" {
		modelName, err := resolveConfiguredModel(cfg, cfg.DefaultModel)
		if err != nil {
			return nil, err
		}
		requirements = append(requirements, modelRequirement{
			Name:  "default_model",
			Model: modelName,
		})
	}
	defaultResolved, err := mode.Resolve(cfg, core.RuntimeRequest{
		MaxSteps:       -1,
		MaxToolRetries: -1,
	})
	if err != nil {
		return nil, err
	}
	requirements = append(requirements, modelRequirement{
		Name:             "default_mode:" + defaultResolved.Name,
		Model:            defaultResolved.Model,
		RequiredFeatures: requiredModelFeatures(defaultResolved, false),
	})

	modeNames := make([]string, 0, len(cfg.Modes))
	for name := range cfg.Modes {
		modeNames = append(modeNames, name)
	}
	sort.Strings(modeNames)

	for _, name := range modeNames {
		resolved, err := mode.Resolve(cfg, core.RuntimeRequest{
			ModeName:       name,
			MaxSteps:       -1,
			MaxToolRetries: -1,
		})
		if err != nil {
			return nil, err
		}
		requirements = append(requirements, modelRequirement{
			Name:             "mode:" + name,
			Model:            resolved.Model,
			RequiredFeatures: requiredModelFeatures(resolved, false),
		})
	}
	return requirements, nil
}

func resolveConfiguredModel(cfg core.Config, value string) (string, error) {
	name := strings.TrimSpace(value)
	if name == "" {
		return "", cerr.New(cerr.ExitCLIUsage, "model is required")
	}
	if resolved, ok := cfg.Models[name]; ok {
		return resolved, nil
	}
	if strings.Contains(name, "/") {
		return name, nil
	}
	return "", cerr.New(cerr.ExitRuntime, fmt.Sprintf("unknown model alias %q", name))
}

func requiredModelFeatures(modeCfg core.ResolvedMode, schemaRequired bool) []string {
	features := make([]string, 0, 3)
	if len(modeCfg.Tools) > 0 {
		features = append(features, "tools")
	}
	if schemaRequired {
		features = append(features, "response_format")
		features = append(features, "structured_outputs")
	}
	return features
}

func (r modelValidationReport) HasFailures() bool {
	for _, check := range r.Checks {
		if check.Error != "" {
			return true
		}
	}
	return false
}

func (r modelValidationReport) FailureMessages() []string {
	out := []string{}
	for _, check := range r.Checks {
		if check.Error == "" {
			continue
		}
		out = append(out, fmt.Sprintf("%s: %s", check.Name, check.Error))
	}
	return out
}
