// SPDX-License-Identifier: GPL-2.0-or-later
package main

import (
	"context"
	"errors"
	"time"

	"github.com/damonto/wwan-go/qcom"
	"github.com/damonto/wwan-go/sim"
)

// This diagnostic never sends a message or changes receive routes. probe-nas
// additionally writes/readbacks PS-only SMS and restores the observed value.
func probeSMS(ctx context.Context, write bool) (result error) {
	client, err := openQCOM(ctx)
	if err != nil {
		return err
	}
	defer client.Close()
	selection, selectionErr := client.SystemSelectionPreference(ctx)
	serving, servingErr := client.NASServingSystem(ctx)
	ready, readyErr := client.WMSServiceReady(ctx)
	domain, domainErr := client.WMSDomainPreference(ctx)
	routes, routesErr := client.WMSGetRoutes(ctx)
	bound, boundErr := client.WMSBoundSubscription(ctx)
	info := map[string]any{"stage": "nas-capabilities", "lte_only": selectionErr == nil && servingErr == nil && ltePacketService(selection, serving),
		"domain_known": domainErr == nil, "routes_known": routesErr == nil, "binding_known": boundErr == nil, "service_ready_known": readyErr == nil && ready.StatusKnown}
	if domainErr == nil {
		info["domain"] = uint8(domain)
	}
	if routesErr == nil {
		info["route_count"] = len(routes.Routes)
		info["reports_known"] = routes.StatusReportKnown
	}
	if boundErr == nil {
		info["bound_subscription"] = uint8(bound)
	}
	if readyErr == nil && ready.StatusKnown {
		info["service_ready"] = uint32(ready.Status)
	}
	emit(info)
	if !write {
		return nil
	}
	if selectionErr != nil || servingErr != nil || !ltePacketService(selection, serving) {
		return failed("nas-probe-lte", errors.New("LTE-only packet service unavailable"))
	}
	if domainErr != nil {
		return failed("nas-probe-domain", domainErr)
	}
	reader, err := sim.NewQCOM(client)
	if err != nil {
		return failed("nas-probe-sim", err)
	}
	scope, err := currentSIMScope(ctx, reader)
	if err != nil {
		return failed("nas-probe-sim", err)
	}
	if err := verifySIMSession(ctx, client, reader, scope); err != nil {
		return failed("nas-probe-sim", err)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		if err := verifySIMSession(cleanup, client, reader, scope); err != nil {
			result = failed("nas-probe-sim-changed", err)
			return
		}
		current, err := client.WMSDomainPreference(cleanup)
		if err != nil {
			result = failed("nas-probe-restore", err)
			return
		}
		if current != domain && current != qcom.WMSDomainPreferencePSOnly {
			result = failed("nas-probe-ownership-lost", errors.New("SMS domain changed by another owner"))
			return
		}
		if current != domain {
			err = client.WMSSetDomainPreference(cleanup, domain)
		}
		if err != nil {
			result = failed("nas-probe-restore", err)
			return
		}
		actual, err := client.WMSDomainPreference(cleanup)
		if err != nil || actual != domain {
			result = failed("nas-probe-restore", errors.New("SMS domain restoration unconfirmed"))
			return
		}
		emit(map[string]any{"stage": "nas-probe-restore", "state": "restored"})
	}()
	// PS-only=3 (PS-preferred=1 is insufficient: it permits CS fallback).
	if err := client.WMSSetDomainPreference(ctx, qcom.WMSDomainPreferencePSOnly); err != nil {
		return failed("nas-probe-write", err)
	}
	actual, err := client.WMSDomainPreference(ctx)
	if err != nil || actual != qcom.WMSDomainPreferencePSOnly {
		return failed("nas-probe-readback", errors.New("PS-only write unconfirmed"))
	}
	emit(map[string]any{"stage": "nas-probe-readback", "state": "confirmed", "ps_only": true})
	return nil
}
