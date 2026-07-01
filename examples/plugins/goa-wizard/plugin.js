// Goa Wizard — Ralph Loop Development Workflow
// Registers a /wizard command that guides the user through
// a structured plan→code→test→compare loop with reviews.
//
// This is an example plugin demonstrating the JS bridge APIs:
//   - goa.registerCommand() — register /wizard command
//   - goa.logger() — logging interface
//   - goa.callTool() — invoke tools by name (requires request_review tool)

goa.registerCommand({
  name: "wizard",
  aliases: ["w"],
  shortHelp: "Start the Ralph-loop development wizard",
  longHelp: "Usage: /wizard <request>\n\n" +
    "Starts an interactive development workflow:\n" +
    "1. Plan & review the change\n" +
    "2. Code & review the change\n" +
    "3. Test & log issues\n" +
    "4. If issues: restart loop\n" +
    "5. Compare result to original request\n" +
    "6. If gap: restart plan/review",
  run: function(args) {
    var request = args.join(" ");
    if (!request) {
      return "Usage: /wizard <development request>";
    }
    // Run asynchronously, return status
    runRalphLoopAsync(request);
    return "Ralph loop started for: " + request;
  }
});

// Runs the Ralph loop in a background goroutine via callTool.
// Each step invokes a registered tool and logs progress.
function runRalphLoopAsync(request) {
  var maxCycles = 5;
  var originalRequest = request;
  var logger = goa.logger();
  var currentRequest = request;

  for (var cycle = 0; cycle < maxCycles; cycle++) {
    logger.info("Ralph loop cycle " + (cycle + 1) + "/" + maxCycles);

    // Phase 1: Plan & review via request_review tool
    var plan = callToolSafe("request_review", {
      content: "Plan the implementation for: " + currentRequest
    });
    logger.info("Plan reviewed.");

    // Phase 2: Code & review
    var code = callToolSafe("request_review", {
      content: "Implement this plan and review the code:\n" + JSON.stringify(plan)
    });
    logger.info("Code reviewed.");

    // Phase 3: Test via run_tests tool (if available)
    var testResult = callToolSafe("run_tests", {
      command: "go test ./..."
    });
    var hasIssues = testResult && testResult.indexOf("FAIL") >= 0;
    logger.info("Test result: " + (hasIssues ? "FAILURES" : "PASS"));

    if (hasIssues) {
      currentRequest = "Fix the issues and retry. Issues:\n" + testResult;
      continue;
    }

    // Phase 4: Compare to original request
    var comparison = callToolSafe("request_review", {
      content: "Compare this implementation to the original request.\n" +
        "Original: " + originalRequest + "\n" +
        "Implementation: " + JSON.stringify(code) + "\n" +
        "Are there any gaps?"
    });

    var hasGap = comparison && (
      comparison.indexOf("gap") >= 0 ||
      comparison.indexOf("missing") >= 0
    );

    if (!hasGap) {
      logger.info("Ralph loop complete. Request fully implemented.");
      return;
    }

    currentRequest = "Address the gaps: " + JSON.stringify(comparison);
  }

  logger.info("Ralph loop reached max cycles. Review the result manually.");
}

// Safely call a tool, returning null on error.
function callToolSafe(name, params) {
  try {
    var result = goa.callTool(name, params);
    return result;
  } catch (e) {
    var logger = goa.logger();
    logger.warn("Tool call failed: " + name + " - " + e);
    return null;
  }
}
