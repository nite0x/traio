package ibkr

import (
	"context"
	"fmt"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/pquerna/otp/totp"
)

// autoLogin performs headless browser login with TOTP 2FA.
// Selectors match IBKR Client Portal Gateway SSO login page; verify against live HTML if login fails.
func (g *GatewayManager) autoLogin(ctx context.Context) error {
	code, err := totp.GenerateCode(g.config.TOTPSecret, time.Now())
	if err != nil {
		return fmt.Errorf("generate totp: %w", err)
	}

	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", true),
		chromedp.Flag("ignore-certificate-errors", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-sandbox", true),
	)
	allocCtx, allocCancel := chromedp.NewExecAllocator(context.Background(), opts...)
	defer allocCancel()

	browserCtx, browserCancel := chromedp.NewContext(allocCtx)
	defer browserCancel()

	loginCtx, loginCancel := context.WithTimeout(browserCtx, 2*time.Minute)
	defer loginCancel()

	loginURL := g.config.GatewayURL + "/sso/Login"
	return chromedp.Run(loginCtx,
		chromedp.Navigate(loginURL),
		chromedp.WaitVisible(`#user_name`, chromedp.ByID),
		chromedp.SendKeys(`#user_name`, g.config.SubAccount, chromedp.ByID),
		chromedp.WaitVisible(`#password`, chromedp.ByID),
		chromedp.SendKeys(`#password`, g.config.Password, chromedp.ByID),
		chromedp.Click(`#submitForm`, chromedp.ByID),
		chromedp.WaitVisible(`#twofacode`, chromedp.ByID),
		chromedp.SendKeys(`#twofacode`, code, chromedp.ByID),
		chromedp.Click(`#submitForm`, chromedp.ByID),
		chromedp.WaitVisible(`#div-auth-status`, chromedp.ByID),
	)
}
