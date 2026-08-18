package command

import "github.com/spf13/cobra"

const configWriteAnnotation = "secret-protector/config-write"

func markConfigWrite(command *cobra.Command) *cobra.Command {
	if command.Annotations == nil {
		command.Annotations = make(map[string]string)
	}
	command.Annotations[configWriteAnnotation] = "true"
	command.Short += " [write]"

	return command
}

func writesConfig(command *cobra.Command) bool {
	return command.Annotations[configWriteAnnotation] == "true"
}
