package java

// Inline fixtures: real native checkstyle output captured by running the tool in
// the image (workdir /workspace); the *ExitCode consts are the recorded
// exit statuses of those runs.

const cleanExitCode = 0

const cleanStdout = `<?xml version="1.0" encoding="UTF-8"?>
<checkstyle version="10.21.1">
<file name="/workspace/Solution.java">
</file>
</checkstyle>
`

const crashExitCode = 254

const crashStderr = `com.puppycrawl.tools.checkstyle.api.CheckstyleException: Exception was thrown while processing Solution.java
	at com.puppycrawl.tools.checkstyle.Checker.processFiles(Checker.java:312)
	at com.puppycrawl.tools.checkstyle.Checker.process(Checker.java:226)
	at com.puppycrawl.tools.checkstyle.Main.runCheckstyle(Main.java:415)
	at com.puppycrawl.tools.checkstyle.Main.runCli(Main.java:338)
	at com.puppycrawl.tools.checkstyle.Main.execute(Main.java:195)
	at com.puppycrawl.tools.checkstyle.Main.main(Main.java:130)
Caused by: com.puppycrawl.tools.checkstyle.api.CheckstyleException: IllegalStateException occurred while parsing file /workspace/Solution.java.
	at com.puppycrawl.tools.checkstyle.JavaParser.parse(JavaParser.java:104)
	at com.puppycrawl.tools.checkstyle.TreeWalker.processFiltered(TreeWalker.java:195)
	at com.puppycrawl.tools.checkstyle.api.AbstractFileSetCheck.process(AbstractFileSetCheck.java:101)
	at com.puppycrawl.tools.checkstyle.Checker.processFile(Checker.java:340)
	at com.puppycrawl.tools.checkstyle.Checker.processFiles(Checker.java:299)
	... 5 more
Caused by: java.lang.IllegalStateException: 2:0: mismatched input '<EOF>' expecting '}'
	at com.puppycrawl.tools.checkstyle.JavaParser$CheckstyleErrorListener.syntaxError(JavaParser.java:254)
	at org.antlr.v4.runtime.ProxyErrorListener.syntaxError(ProxyErrorListener.java:41)
	at org.antlr.v4.runtime.Parser.notifyErrorListeners(Parser.java:544)
	at org.antlr.v4.runtime.DefaultErrorStrategy.reportInputMismatch(DefaultErrorStrategy.java:327)
	at org.antlr.v4.runtime.DefaultErrorStrategy.reportError(DefaultErrorStrategy.java:139)
	at com.puppycrawl.tools.checkstyle.CheckstyleParserErrorStrategy.recoverInline(CheckstyleParserErrorStrategy.java:38)
	at org.antlr.v4.runtime.Parser.match(Parser.java:208)
	at com.puppycrawl.tools.checkstyle.grammar.java.JavaLanguageParser.classBody(JavaLanguageParser.java:2490)
	at com.puppycrawl.tools.checkstyle.grammar.java.JavaLanguageParser.classDeclaration(JavaLanguageParser.java:1101)
	at com.puppycrawl.tools.checkstyle.grammar.java.JavaLanguageParser.types(JavaLanguageParser.java:758)
	at com.puppycrawl.tools.checkstyle.grammar.java.JavaLanguageParser.typeDeclaration(JavaLanguageParser.java:672)
	at com.puppycrawl.tools.checkstyle.grammar.java.JavaLanguageParser.compilationUnit(JavaLanguageParser.java:419)
	at com.puppycrawl.tools.checkstyle.JavaParser.parse(JavaParser.java:98)
	... 9 more
Caused by: org.antlr.v4.runtime.InputMismatchException
	... 17 more
Checkstyle ends with 1 errors.
`

const dirtyExitCode = 2

const dirtyStdout = `<?xml version="1.0" encoding="UTF-8"?>
<checkstyle version="10.21.1">
<file name="/workspace/Solution.java">
<error line="1" column="17" severity="error" message="Using the &apos;.*&apos; form of import should be avoided - java.util.*." source="com.puppycrawl.tools.checkstyle.checks.imports.AvoidStarImportCheck"/>
<error line="5" column="9" severity="error" message="&apos;if&apos; construct must use &apos;{}&apos;s." source="com.puppycrawl.tools.checkstyle.checks.blocks.NeedBracesCheck"/>
</file>
</checkstyle>
`

const dirtyStderr = `Checkstyle ends with 2 errors.
`
