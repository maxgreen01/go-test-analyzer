# Go Test Analysis Engine

Multi-stage tool for automatically detecting, analyzing, and refactoring test cases in Go projects via static analysis and AST manipulation.

[![License](https://img.shields.io/github/license/maxgreen01/go-test-analyzer)](LICENSE)
[![Release](https://img.shields.io/github/v/release/maxgreen01/go-test-analyzer)](https://github.com/maxgreen01/go-test-analyzer/releases)
[![DOI](https://zenodo.org/badge/994874951.svg)](https://doi.org/10.5281/zenodo.18177436)

## Features

- Detects and analyzes Go test functions across entire projects.
- Generates detailed reports on project-wide testing statistics or comprehensive analyses of individual tests.
- Supports multiple output formats, including CSV and plaintext.
- Provides structured logging to both the terminal and the dedicated logfile `analyzer.log`.

## Quick Start

Download the latest executable binary for your operating system from the [Releases](https://github.com/maxgreen01/go-test-analyzer/releases) page. All binaries are named like `go-test-analyzer-<version>-<platform>`, but you can rename them for convenience. Throughout this documentation, the `-<version>-<platform>` part of the executable name is omitted for brevity.

To use the program, run it in the command line using the following format:

```bash
./go-test-analyzer <command> [options]
```

For a list of all supported commands, see the [Commands](#commands) section.

For a list of global command-line options, see the [Application Options](#application-options) section. Also see the Command Options subsection of each command's documentation to view their specific options.

### Building from Source

If an executable binary for your operating system or architecture is not provided, you can build one yourself from the project source using the Makefile.

First, download the source code by cloning the repository using `git clone` or by downloading it as a ZIP file from the [Releases](https://github.com/maxgreen01/go-test-analyzer/releases) page. [Go](https://go.dev/) version 1.24.5 or newer is required to build this program, but the latest release is recommended.

To build the executable for your system, navigate to the project directory and run the following command (instead of executing `go build` directly):

```bash
make
```

This will create a new executable binary in the `build` directory named `go-test-analyzer-<version>`. To check that the program was built successfully, you can run the version command like `./go-test-analyzer version` from the `build` directory using the new executable.

If you want to cross-compile the program for a different platform, you can use the `cross-compile` target to automatically build executables for Windows, Linux, and macOS on both AMD64 and ARM64 architectures.

```bash
make cross-compile
```

If you only want to build for one of these platforms, you can use the corresponding target (e.g. `windows-amd64`, `linux-arm64`, etc.) instead.

```bash
make windows-amd64  # Cross-compile for Windows on AMD64 architecture
make linux-arm64    # Cross-compile for Linux on ARM64 architecture
```

If you need to build for a different OS or architecture not included in the [Makefile](https://github.com/maxgreen01/go-test-analyzer/blob/main/Makefile), you can set the `GOOS` and `GOARCH` environment variables to the desired values and then run `make`. To view all of Go's supported platforms, run `go tool dist list`. For example, on UNIX systems, you can compile for a different platform by running:

```bash
GOOS=freebsd GOARCH=amd64 make  # Cross-compile for FreeBSD on AMD64 architecture
```

## Application Options

Below is a list of the command-line options supported by the application:

| Option              | Description                                                                            | Default Value | Example Argument                               |
| ------------------- | -------------------------------------------------------------------------------------- | ------------- | ---------------------------------------------- |
| `--project` / `-p`  | Path to the Go project directory to be analyzed                                        | Required      | `C:/programs/my-go-project`, `./other-project` |
| `--output` / `-o`   | Path to report output file                                                             | Required      | `./output/report.csv`, `stats-report.txt`      |
| `--append`          | Whether to append to the output file instead of overwriting it                         | `false`       | N/a                                            |
| `--splitByDir`      | Whether to analyze each top-level directory separately                                 | `false`       | N/a                                            |
| `--threads`         | The number of concurrent threads to use for parsing (only when splitting by directory) | `4`           | `2`, `8`                                       |
| `--logLevel` / `-l` | The minimum severity of log message that should be displayed                           | `info`        | `debug`, `info`, `warn`, `error` (exhaustive)  |

To access the help menu and see all available options, run:

```bash
./go-test-analyzer --help
```

To view any command-specific options in addition to the global ones, run:

```bash
./go-test-analyzer <command> --help
```

## Commands

### Statistics

The `statistics` command analyzes the Go test files in the specified project directory and generates various statistics related to the project's test cases. This includes metrics such as the total number of test cases, number of test files, average test length, and the percentage of the project comprised of test code (by lines).

Supports output to either `.txt` or `.csv` files. Output is especially well-suited for a `.csv` file if using the `splitByDir` option.

Example:

```bash
./go-test-analyzer statistics --project ./my-go-project --output ./output/statistics-report.csv
```

### Analyze

The `analyze` command performs a deeper analysis of the test cases in a project. This command identifies various structural elements in each test, with a focus on table-driven tests. The results of analyzing the tests are saved in their own JSON files, which are put in a new folder in the same directory as the `output` file. The JSON files are named like `<project>/<project>_<package>_<testName>.json`.

Certain detected test cases can also be refactored using the `refactor` option, as described in the [Command Options](#analyze-command-options) subsection.

Supports output to either `.txt` or `.csv` files. Output is especially well-suited for a `.csv` file because it will contain a condensed version of the analysis results of every test case.

Example:

```bash
./go-test-analyzer analyze --project ./my-go-project --output ./output/analyze-report.csv
```

#### Analyze Command Options

The following command-line options are only supported by the `analyze` command.

| Option                    | Description                                                                                                         | Default Value | Example Argument               |
|---------------------------|---------------------------------------------------------------------------------------------------------------------|---------------|--------------------------------|
| `--refactor`              | The type of refactoring to perform on the detected test cases. See below for additional details                     | `none`        | `none`, `subtest` (exhaustive) |
| `--keep-refactored-files` | Whether to retain the results of refactored test cases by NOT restoring the original source files after refactoring | `false`       | N/a                            |

The `refactor` option indicates which type of refactoring should be performed on certain detected test cases. After refactoring, the refactored function is saved as a field in the JSON output file for each affected test case. Note that the refactoring may modify helper functions defined in the same package, but these are not reflected in the JSON output. The allowed refactoring strategies are described as follows:

- The `none` argument indicates that no refactoring will be performed.
- The `subtest` refactoring method affects tests that are detected to be table-driven but do not use `t.Run()` to declare subtests. The refactoring wraps the entire contents of the execution loop in a `t.Run()` call, using the detected scenario name field (or a stringified version of one of the input fields) as the subtest name.

The `keep-refactored-files` option allows the user to review the refactored code directly in their original files. The program's default behavior is to revert refactored code to its original state after refactoring is complete, but this option disables that behavior. If you plan to run the analyzer multiple times on the same project, you must restore the original files before each run to ensure accurate results! To restore the original files, you can use Git to revert the changes or back up the original files before running the analyzer.

Note that if this option is enabled, compilation errors caused by a refactoring will likely affect the execution results (but not the actual refactorings) of other tests in the same file. Also, if multiple tests perform a refactoring on the same helper function, the final state of the code will depend solely on the last refactoring attempt that affected the helper.

### Version

Prints the program's version number, the build target OS and architecture, and the Go version used to build the program.

For example:

```bash
$ ./go-test-analyzer version
go-test-analyzer v1.2.1
- os/type: windows
- os/arch: amd64
- go/version: go1.24.5
```

## Contributing

Contributions are welcome! Please feel free to submit [Issues](https://github.com/maxgreen01/go-test-analyzer/issues) or [Pull Requests](https://github.com/maxgreen01/go-test-analyzer/issues)!

## License

This project is licensed under the MIT License. See the [LICENSE](https://github.com/maxgreen01/go-test-analyzer/blob/main/LICENSE) file for details.
