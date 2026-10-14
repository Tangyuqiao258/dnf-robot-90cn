package webadmin

import (
	_ "embed"
	"html/template"
	"strings"
)

const (
	appCSSPlaceholder = "<!-- APP_CSS -->"
	i18nJSPlaceholder = "<!-- I18N_JS -->"
	appJSPlaceholder  = "<!-- APP_JS -->"
)

//go:embed assets/login.html
var loginHTML string

//go:embed assets/index.html
var indexHTML string

//go:embed assets/app.css
var appCSS string

//go:embed assets/i18n.js
var i18nJS string

// The script and style assets are inlined into the page *before* it is handed
// to html/template, and that parser drops comments from the script and style
// contexts. An asset carrying a comment therefore does not reach the browser
// the way it reads on disk. Keep assets/app.js and assets/i18n.js free of
// comments; TestIndexTemplateInlinesEmbeddedAssets fails if one creeps back in.
//
//go:embed assets/app.js
var appJS string

var cleanLoginTemplate = template.Must(template.New("clean_login").Parse(inlineLoginAssets()))
var cleanIndexTemplate = template.Must(template.New("clean_index").Parse(inlineIndexAssets()))

func inlineLoginAssets() string {
	if strings.Count(loginHTML, i18nJSPlaceholder) != 1 {
		panic("webadmin: login i18n asset placeholder must occur exactly once")
	}
	return strings.Replace(trimAssetTerminator(loginHTML), i18nJSPlaceholder, "<script>\n"+trimAssetTerminator(i18nJS)+"\n</script>", 1)
}

func inlineIndexAssets() string {
	if strings.Count(indexHTML, appCSSPlaceholder) != 1 || strings.Count(indexHTML, i18nJSPlaceholder) != 1 || strings.Count(indexHTML, appJSPlaceholder) != 1 {
		panic("webadmin: index asset placeholders must each occur exactly once")
	}
	return strings.NewReplacer(
		appCSSPlaceholder, "<style>\n"+trimAssetTerminator(appCSS)+"\n</style>",
		i18nJSPlaceholder, "<script>\n"+trimAssetTerminator(i18nJS)+"\n</script>",
		appJSPlaceholder, "<script>\n"+trimAssetTerminator(appJS)+"\n</script>",
	).Replace(trimAssetTerminator(indexHTML))
}

func trimAssetTerminator(content string) string {
	content = strings.TrimSuffix(content, "\n")
	return strings.TrimSuffix(content, "\r")
}
