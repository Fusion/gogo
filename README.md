## gogo

### What does it do?

Here is why I wrote this tool: I love the idea of being able to roll out my favorite shell tools locally anywhere I need to.

This means I am a fan of self-sufficient tools, such as lazygit and others. No library dependencies, and as OS-independent as possible.

So, please **do not call this tool a package manager** as that is a non-goal.

All it does is, provided a minimal list of hints, it will download and install whatever commands I need:
- as long as there are no dependencies
- as long as they are available as packages on github

A few examples: 
- [tldr](https://github.com/isacikgoz/tldr)
- [lazygit](https://github.com/jesseduffield/lazygit)
- [lazysql](https://github.com/jorgerojas26/lazysql)
- [certinfo](https://github.com/pete911/certinfo)
- [croc](https://github.com/schollz/croc)


### Typical workflow

First setup:

1. Download `gogo` and install it in your path
2. Create or re-use an existing configuration
3. Run `gogo fetch -config <path-to-configuration>`

Updating a single command:

1. Confirm command name using `gogo list -config <path-to-configuration>`
2. Run `goto fetch <command-name> -config <path-to-configuration> -update`

Installing missing commands:

1. Update configuration to include these commands
2. Run `gogo fetch -config <path-to-configuration>`

Refresh all commands:

1. Run `goto fetch -config <path-to-configuration> -update`

### Specifying where the commands should go

If you leave this location unspecified, these commands will be located in the same directory as this tool itself.

To specify a different location, add to your configuration file/directory:

```
[paths]
targetdir = "<path>"
```

### Working around GitHub's rate limiter

If you are running this tool as an anonymous user, you will be able to perform up to 60 queries per hour. If should be enough for many use cases.

If you need greater API allowance, follow [this guide](https://docs.github.com/en/authentication/keeping-your-account-and-data-secure/managing-your-personal-access-tokens) to create personal access tokens. 

Note that you will need to grant your token specific repo access if you plan on getting commands from private repositories.

Store your token in the configuration file/directory:

```
[auth]
token = "github_<xxxxxxxxxx>"
```

