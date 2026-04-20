package reports

import (
	"bytes"
	"fmt"
	"html/template"
)

// monthlyEmailTpl: template HTML embebido. Usa inline CSS porque la mayoría
// de los clientes de email ignora o sanitiza <style>. Mantenemos el diseño
// muy simple (tipografía sistema, tablas) para maximizar compatibilidad.
var monthlyEmailTpl = template.Must(template.New("monthly").Parse(`<!DOCTYPE html>
<html lang="es">
<head><meta charset="utf-8"></head>
<body style="font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif; color:#1a1a1a; background:#f5f5f7; padding: 24px;">
  <div style="max-width:640px; margin:0 auto; background:#fff; border-radius:12px; padding:32px;">
    <h1 style="font-size:20px; margin:0 0 8px 0;">Ahorra — Resumen mensual</h1>
    <p style="color:#666; margin:0 0 24px 0;">{{.HouseholdName}} · {{.Month}} · {{.BaseCurrency}}</p>

    <table style="width:100%; border-collapse:collapse; margin-bottom:24px;">
      <tr>
        <td style="padding:12px; background:#f5f5f7; border-radius:8px; width:33%;">
          <div style="color:#666; font-size:12px;">Gastado</div>
          <div style="font-size:18px; font-weight:600;">{{.Spent}}</div>
        </td>
        <td style="width:8px;"></td>
        <td style="padding:12px; background:#f5f5f7; border-radius:8px; width:33%;">
          <div style="color:#666; font-size:12px;">Resumido</div>
          <div style="font-size:18px; font-weight:600;">{{.Billed}}</div>
        </td>
        <td style="width:8px;"></td>
        <td style="padding:12px; background:#f5f5f7; border-radius:8px; width:33%;">
          <div style="color:#666; font-size:12px;">A pagar</div>
          <div style="font-size:18px; font-weight:600;">{{.Due}}</div>
        </td>
      </tr>
    </table>

    <h2 style="font-size:16px; margin:24px 0 12px 0;">Fijos vs variables</h2>
    <p style="margin:0 0 16px 0; color:#444;">
      Fijos: <strong>{{.FixedTotal}}</strong> ({{.FixedPct}}%) ·
      Variables: <strong>{{.VariableTotal}}</strong> ({{.VariablePct}}%)
    </p>

    <h2 style="font-size:16px; margin:24px 0 12px 0;">Top categorías</h2>
    <table style="width:100%; border-collapse:collapse;">
      {{range .Categories}}
      <tr>
        <td style="padding:8px 0; border-bottom:1px solid #eee;">{{.Name}}</td>
        <td style="padding:8px 0; border-bottom:1px solid #eee; text-align:right;">{{.Total}}</td>
        <td style="padding:8px 0; border-bottom:1px solid #eee; text-align:right; color:#666; width:60px;">{{.Pct}}%</td>
      </tr>
      {{end}}
    </table>

    <p style="margin-top:32px; color:#999; font-size:12px;">
      Montos en {{.BaseCurrency}}. Generado automáticamente por Ahorra.
    </p>
  </div>
</body>
</html>`))

type emailCategory struct {
	Name  string
	Total string
	Pct   string
}

type emailData struct {
	HouseholdName string
	Month         string
	BaseCurrency  string
	Spent         string
	Billed        string
	Due           string
	FixedTotal    string
	VariableTotal string
	FixedPct      string
	VariablePct   string
	Categories    []emailCategory
}

// RenderMonthlyHTML arma el HTML del email a partir del MonthlyReport.
// Los números se formatean con el símbolo de la currency ya pegado para
// que el template sea agnóstico.
func RenderMonthlyHTML(hhName string, rep MonthlyReport) (string, error) {
	cur := rep.BaseCurrency
	fmtMoney := func(v float64) string { return fmt.Sprintf("%.2f %s", v, cur) }
	fmtPct := func(v float64) string { return fmt.Sprintf("%.1f", v) }

	cats := make([]emailCategory, 0, len(rep.ByCategory))
	for i, c := range rep.ByCategory {
		if i >= 10 {
			break
		}
		cats = append(cats, emailCategory{
			Name:  c.CategoryName,
			Total: fmtMoney(c.Total),
			Pct:   fmtPct(c.Pct),
		})
	}

	data := emailData{
		HouseholdName: hhName,
		Month:         rep.Month,
		BaseCurrency:  cur,
		Spent:         fmtMoney(rep.Spent),
		Billed:        fmtMoney(rep.Billed),
		Due:           fmtMoney(rep.Due),
		FixedTotal:    fmtMoney(rep.FixedVariable.FixedTotal),
		VariableTotal: fmtMoney(rep.FixedVariable.VariableTotal),
		FixedPct:      fmtPct(rep.FixedVariable.FixedPct),
		VariablePct:   fmtPct(rep.FixedVariable.VariablePct),
		Categories:    cats,
	}

	var buf bytes.Buffer
	if err := monthlyEmailTpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("reports: render email: %w", err)
	}
	return buf.String(), nil
}
