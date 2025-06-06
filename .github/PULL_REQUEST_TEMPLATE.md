<!--  Thanks for sending a pull request!  Here are some tips for you:
1. Ensure you have added the unit tests for your changes.
2. Ensure you have included output of manual testing done in the Testing section.
3. Ensure number of lines of code for new or existing methods are within the reasonable limit.
4. Ensure your change works on existing clusters after upgrade.
5. If your mounting any new file or directory, make sure its not opening up any security attack vector for aws-application-networking-k8s modules.
6. If AWS apis are invoked, document the call rate in the description section.
7. If EC2 Metadata apis are invoked, ensure to handle stale information returned from metadata.
-->
**What type of PR is this?**

<!--
Add one of the following:
bug
cleanup
documentation
feature
-->

**Which issue does this PR fix**:


**What does this PR do / Why do we need it**:


**If an issue # is not available please add repro steps and logs from aws-gateway-controller showing the issue**:


**Testing done on this change**:
<!--
output of manual testing/integration tests results and also attach logs
showing the fix being resolved
-->

**Automation added to e2e**:
<!--
Test case added to lib/integration.sh
If no, create an issue with enhancement/testing label
-->

**Will this PR introduce any new dependencies?**:
<!--
e.g. new EC2/K8s API, IMDS API, dependency on specific kernel module/version or binary in container OS.
-->

**Will this break upgrades or downgrades. Has updating a running cluster been tested?**:

**Does this PR introduce any user-facing change?**:
<!--
If yes, a release note update is required:
Enter your extended release note in the block below. If the PR requires additional actions
from users switching to the new release, include the string "action required".
-->

```release-note

```

**Do all end-to-end tests successfully pass when running `make e2e-test`?**:
<!--
Please provide a snippet of a successful `make e2e-test` run to confirm.
-->
```

```

By submitting this pull request, I confirm that my contribution is made under the terms of the Apache 2.0 license.