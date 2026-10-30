package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/alvaroborras/alcampo-cli/internal/client"
)

func cmdCheckout(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: alcampo checkout <addresses|slots|reserve|summary|finish> [flags]")
	}
	sub, rest := args[0], args[1:]
	fs := flag.NewFlagSet("checkout", flag.ExitOnError)
	cf := addCommon(fs)
	addressID := fs.String("address", "", "delivery address (delivery destination) id; default: the cart's")
	slotID := fs.String("slot", "", "delivery slot id (from `checkout slots`)")
	maxFlag := fs.Float64("max", 0, "refuse a checkout whose total exceeds this many € (0 = env/config)")
	_ = fs.Parse(reorderArgs(fs, rest))
	cl, err := authedClient(cf)
	if err != nil {
		return err
	}
	switch sub {
	case "addresses":
		raw, err := cl.Addresses()
		if err != nil {
			return err
		}
		return emitRaw(raw)
	case "slots":
		cart, _, err := cl.GetCart()
		if err != nil {
			return err
		}
		addr := firstNonEmpty(*addressID, cart.DeliveryDestinationID)
		if addr == "" {
			return fmt.Errorf("need --address <id> (from `alcampo checkout addresses`)")
		}
		raw, err := cl.Slots(addr, cart.RegionID, cart.DefaultCheckoutGroup.ShippingGroupType)
		if err != nil {
			return err
		}
		return emitRaw(raw)
	case "reserve":
		if *slotID == "" {
			return fmt.Errorf("need --slot <id> (from `alcampo checkout slots`)")
		}
		cart, _, err := cl.GetCart()
		if err != nil {
			return err
		}
		addr := firstNonEmpty(*addressID, cart.DeliveryDestinationID)
		if addr == "" {
			return fmt.Errorf("need --address <id> (from `alcampo checkout addresses`)")
		}
		raw, err := cl.ReserveSlot(*slotID, addr, cart.RegionID)
		if err != nil {
			return err
		}
		return emitRaw(raw)
	case "summary":
		raw, err := cl.CheckoutSummary()
		if err != nil {
			return err
		}
		if bErr := budgetCheckRaw(raw, *maxFlag, "checkout"); bErr != nil {
			return bErr
		}
		return emitRaw(raw)
	case "finish":
		// Payment runs through the shop's card processor with 3-D Secure, which
		// needs the browser. So the CLI's last step is the budget gate: verify
		// the payable total against the cap, then hand off the checkout URL.
		raw, err := cl.CheckoutSummary()
		if err != nil {
			return fmt.Errorf("couldn't read the checkout summary: %w", err)
		}
		maxEUR, mErr := resolveMax(*maxFlag)
		if mErr != nil {
			return mErr
		}
		total, ok := client.ExtractTotal(raw)
		if bErr := checkBudget(total, ok, maxEUR, "check out", true); bErr != nil {
			return bErr
		}
		if cf.jsonOut {
			return emitJSON(map[string]any{"total": total, "total_known": ok, "max_eur": maxEUR, "checkout_url": cl.CheckoutURL()})
		}
		if ok {
			fmt.Fprintf(os.Stderr, "budget ok: total %.2f€ (limit %s)\n", total, limitStr(maxEUR))
		}
		fmt.Printf("→ review and pay in the browser (same account): %s\n", cl.CheckoutURL())
		return nil
	default:
		return fmt.Errorf("unknown checkout subcommand %q (addresses|slots|reserve|summary|finish)", sub)
	}
}

func limitStr(maxEUR float64) string {
	if maxEUR <= 0 {
		return "none"
	}
	return fmt.Sprintf("%.2f€", maxEUR)
}
