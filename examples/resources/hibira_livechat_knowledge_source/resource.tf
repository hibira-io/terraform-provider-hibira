# The AI assistant answers from the site's llms-full.txt, llms.txt or
# sitemap, kept up to date.
resource "hibira_livechat_knowledge_source" "help_center" {
  workspace_id = "ws_0123456789abcdef"
  url          = "https://help.example.com/"
}
