package executor

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/cynkra/daggle/dag"
)

// ConnectExecutor deploys content to Posit Connect via the rsconnect R package.
type ConnectExecutor struct{}

// Run generates R code to deploy to Posit Connect and executes it via Rscript.
func (e *ConnectExecutor) Run(ctx context.Context, step dag.Step, logDir string, workdir string, env []string) Result {
	rCode := wrapErrorOn(generateConnectR(step.Connect), step.ErrorOn)
	return runRScript(ctx, rCode, step, logDir, workdir, env, "connect")
}

func generateConnectR(c *dag.ConnectDeploy) string {
	forceUpdate := "TRUE"
	if c.ForceUpdate != nil && !*c.ForceUpdate {
		forceUpdate = "FALSE"
	}

	appName := c.Name
	if appName == "" {
		// Default to last path component
		appName = filepath.Base(strings.TrimRight(c.Path, "/"))
	}

	var deployCall string
	switch c.Type {
	case "shiny":
		deployCall = fmt.Sprintf(`rsconnect::deployApp(
  appDir = %q,
  appName = %q,
  account = acct_name,
  server = server_name,
  forceUpdate = %s,
  launch.browser = FALSE
)`, c.Path, appName, forceUpdate)
	case "quarto":
		deployCall = fmt.Sprintf(`rsconnect::deployDoc(
  doc = %q,
  appName = %q,
  account = acct_name,
  server = server_name,
  forceUpdate = %s,
  launch.browser = FALSE
)`, c.Path, appName, forceUpdate)
	case "plumber":
		deployCall = fmt.Sprintf(`rsconnect::deployAPI(
  api = %q,
  appName = %q,
  account = acct_name,
  server = server_name,
  forceUpdate = %s,
  launch.browser = FALSE
)`, c.Path, appName, forceUpdate)
	}

	return fmt.Sprintf(`# Validate environment
server <- Sys.getenv("CONNECT_SERVER")
api_key <- Sys.getenv("CONNECT_API_KEY")

if (nchar(server) == 0) stop("CONNECT_SERVER environment variable is not set")
if (nchar(api_key) == 0) stop("CONNECT_API_KEY environment variable is not set")

if (!requireNamespace("rsconnect", quietly = TRUE)) stop("step requires the rsconnect package. Install with: install.packages('rsconnect')")

library(rsconnect)

# Derive a server name from the URL
server_name <- sub("^https?://", "", server)
server_name <- sub("/+$", "", server_name)
server_name <- gsub("[^a-zA-Z0-9]", "_", server_name)
acct_name <- server_name

# Register the Connect server
tryCatch(rsconnect::removeServer(name = server_name), error = function(e) invisible(NULL))
rsconnect::addConnectServer(url = server, name = server_name)
rsconnect::connectApiUser(account = acct_name, server = server_name, apiKey = api_key)

cat("Deploying to", server, "\n")

%s

cat(paste0("::daggle-output name=connect_url::", server, "\n"))
cat("::daggle-output name=connect_app::%s\n")
cat("Deploy complete\n")
`, deployCall, appName)
}
