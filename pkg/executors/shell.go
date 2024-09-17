package executors

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"github.com/go-cmd/cmd"

	"github.com/lunarway/shuttle/pkg/config"
	"github.com/lunarway/shuttle/pkg/errors"
	"github.com/lunarway/shuttle/pkg/telemetry"
	"github.com/lunarway/shuttle/pkg/ui"
)

func ShellExecutor(action config.ShuttleAction) (Executor, bool) {
	return executeShell, action.Shell != ""
}

// Build builds the docker image from a shuttle plan
func executeShell(ctx context.Context, ui *ui.UI, context ActionExecutionContext) error {
	cmdOptions := cmd.Options{
		Buffered:  false,
		Streaming: true,
		// support large outputs from scripts
		LineBufferSize: 512e3,
	}

	cmdArgs := []string{
		"-c",
		fmt.Sprintf("cd '%s'; %s", context.ScriptContext.Project.ProjectPath, context.Action.Shell),
	}
	execCmd := cmd.NewCmdOptions(cmdOptions, "sh", cmdArgs...)

	context.ScriptContext.Project.UI.Verboseln(
		"Starting shell command: %s %s",
		execCmd.Name,
		strings.Join(cmdArgs, " "),
	)

	err := setupCommandEnvironmentVariables(execCmd, context)
	if err != nil {
		return fmt.Errorf("failed setting up cmd env variables: %w", err)
	}

	execCmd.Env = append(
		execCmd.Env,
		fmt.Sprintf("SHUTTLE_CONTEXT_ID=%s", telemetry.ContextIDFrom(ctx)),
	)

	outputReadCompleted := make(chan struct{})

	go func() {
		defer close(outputReadCompleted)

		for execCmd.Stdout != nil || execCmd.Stderr != nil {
			select {
			case line, open := <-execCmd.Stdout:
				if !open {
					execCmd.Stdout = nil
					continue
				}
				context.ScriptContext.Project.UI.Output("%s", line)
			case line, open := <-execCmd.Stderr:
				if !open {
					execCmd.Stderr = nil
					continue
				}
				context.ScriptContext.Project.UI.Infoln("%s", line)
			}
		}
	}()

	// stop cmd if context is cancelled
	go func() {
		select {
		case <-ctx.Done():
			err := execCmd.Stop()
			if err != nil {
				context.ScriptContext.Project.UI.Errorln(
					"Failed to stop script '%s': %v",
					context.Action.Shell,
					err,
				)
			}
		case <-outputReadCompleted:
		}
	}()

	select {
	case status := <-execCmd.Start():
		<-outputReadCompleted
		if status.Exit > 0 {
			return errors.NewExitCode(
				4,
				"Failed executing script `%s`: shell script `%s`\nExit code: %v",
				context.ScriptContext.ScriptName,
				context.Action.Shell,
				status.Exit,
			)
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func setupCommandEnvironmentVariables(execCmd *cmd.Cmd, context ActionExecutionContext) error {
	shuttlePath, _ := filepath.Abs(filepath.Dir(os.Args[0]))

	// on Windows shell scripts rely on Git Bash, and for path provided as env vars to work in this context
	// they need be in unix format
	shPathForGitBashOnWindows, err := resolveShPathForWindows(context.ScriptContext.Project.ProjectPath)
	if err != nil {
		return err
	}

	execCmd.Env = os.Environ()
	for name, value := range context.ScriptContext.Args {
		execCmd.Env = append(execCmd.Env, fmt.Sprintf("%s=%s", name, value))
	}
	execCmd.Env = append(
		execCmd.Env,
		fmt.Sprintf("shuttle_plan=%s", replaceWindowsPathSegmentIfNeeded(
			context.ScriptContext.Project.ProjectPath,
			shPathForGitBashOnWindows, context.ScriptContext.Project.LocalPlanPath)),
		fmt.Sprintf("plan=%s", replaceWindowsPathSegmentIfNeeded(
			context.ScriptContext.Project.ProjectPath,
			shPathForGitBashOnWindows, context.ScriptContext.Project.LocalPlanPath)),
	)
	execCmd.Env = append(
		execCmd.Env,
		fmt.Sprintf("shuttle_tmp=%s", replaceWindowsPathSegmentIfNeeded(
			context.ScriptContext.Project.ProjectPath,
			shPathForGitBashOnWindows, context.ScriptContext.Project.TempDirectoryPath)),
		fmt.Sprintf("tmp=%s", replaceWindowsPathSegmentIfNeeded(
			context.ScriptContext.Project.ProjectPath,
			shPathForGitBashOnWindows, context.ScriptContext.Project.TempDirectoryPath)),
	)
	execCmd.Env = append(
		execCmd.Env,
		fmt.Sprintf("project=%s", replaceWindowsPathSegmentIfNeeded(
			context.ScriptContext.Project.ProjectPath,
			shPathForGitBashOnWindows, context.ScriptContext.Project.ProjectPath)),
		fmt.Sprintf("shuttle_project=%s", replaceWindowsPathSegmentIfNeeded(
			context.ScriptContext.Project.ProjectPath,
			shPathForGitBashOnWindows, context.ScriptContext.Project.ProjectPath)),
	)
	// TODO: Add project path as a shuttle specific ENV
	execCmd.Env = append(
		execCmd.Env,
		fmt.Sprintf("PATH=%s", shuttlePath+string(os.PathListSeparator)+os.Getenv("PATH")),
	)
	execCmd.Env = append(
		execCmd.Env,
		fmt.Sprintf(
			"SHUTTLE_PLANS_ALREADY_VALIDATED=%s",
			context.ScriptContext.Project.LocalPlanPath,
		),
	)
	execCmd.Env = append(
		execCmd.Env,
		"SHUTTLE_INTERACTIVE=default",
	)
	return nil
}

func resolveShPathForWindows(projectPath string) (string, error) {
	shPathWindows := ""
	if runtime.GOOS == "windows" {
		// cygpath is a tool provided by Git Bash for windows, for converting paths between windows and unix format
		cmd := exec.Command("cygpath")
		// as per the os/exec docs escaping of args on Windows might require using SysProcAttr.CmdLine directly,
		// which is the case in this scenario
		cmd.SysProcAttr = &syscall.SysProcAttr{
			CmdLine: fmt.Sprintf(`cygpath -u "%s"`, projectPath),
		}
		cmd.Env = os.Environ()
		shPath, err := cmd.CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("failed converting windows path to unix style path, %w", err)
		}
		shPathWindows = strings.TrimSuffix(string(shPath), "\n")
	}
	return shPathWindows, nil
}

func replaceWindowsPathSegmentIfNeeded(windowsPathSegment, shPathReplacement, originalPath string) string {
	if runtime.GOOS == "windows" {
		return strings.Replace(originalPath, windowsPathSegment, shPathReplacement, -1)
	}
	return originalPath
}
