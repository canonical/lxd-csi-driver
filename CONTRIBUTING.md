# Contributing

The LXD team welcomes contributions through pull requests, issue reports, and discussions.
- Contribute to the code or documentation by opening a pull request.
- Report bugs or request features in the [LXD GitHub repository](https://github.com/canonical/lxd).
- Ask questions or join discussions in the [LXD forum](https://discourse.ubuntu.com/c/lxd/126).

Review the following guidelines before contributing to the project.

## Code of Conduct

All contributors must adhere to the [Ubuntu Code of Conduct](https://ubuntu.com/community/ethos/code-of-conduct).

## License and copyright

All contributors must sign the [Canonical contributor license agreement (CCLA)](https://ubuntu.com/legal/contributors), which grants Canonical permission to use the contributions.

- You retain copyright ownership of your contributions (no copyright assignment).
- By default, contributions are licensed under the project's **AGPL-3.0-only** license.
- Exceptions:
  - Canonical may import code under AGPL-3.0-only compatible licenses, such as Apache-2.0.
  - Such code retains its original license and is marked as such in commit messages or file headers.
  - Some files and commits may be licensed under Apache-2.0 rather than AGPL-3.0-only. These are indicated in their package-level COPYING file, file header, or commit message.

## Pull requests

Submit pull requests on GitHub at: [`https://github.com/canonical/lxd-csi-driver`](https://github.com/canonical/lxd-csi-driver).

All pull requests undergo review and must be approved before being merged into the main branch.

### Developer Certificate of Origin sign-off

To ensure transparency and accountability in contributions to this project, all contributors must include a **Signed-off-by** line in their commits in accordance with DCO 1.1:

```
Developer Certificate of Origin
Version 1.1

Copyright (C) 2004, 2006 The Linux Foundation and its contributors.
660 York Street, Suite 102,
San Francisco, CA 94110 USA

Everyone is permitted to copy and distribute verbatim copies of this
license document, but changing it is not allowed.

Developer's Certificate of Origin 1.1

By making a contribution to this project, I certify that:

(a) The contribution was created in whole or in part by me and I
    have the right to submit it under the open source license
    indicated in the file; or

(b) The contribution is based upon previous work that, to the best
    of my knowledge, is covered under an appropriate open source
    license and I have the right under that license to submit that
    work with modifications, whether created in whole or in part
    by me, under the same open source license (unless I am
    permitted to submit under a different license), as indicated
    in the file; or

(c) The contribution was provided directly to me by some other
    person who certified (a), (b) or (c) and I have not modified
    it.

(d) I understand and agree that this project and the contribution
    are public and that a record of the contribution (including all
    personal information I submit with it, including my sign-off) is
    maintained indefinitely and may be redistributed consistent with
    this project or the open source license(s) involved.
```

#### Including a Signed-off-by line in your commits

Every commit must include a **Signed-off-by** line, even when part of a larger set of contributions. To do this, use the `-s` flag when committing:

    git commit -s -m "Your commit message"

This automatically adds the following to your commit message:

```
Signed-off-by: Your Name <your.email@example.com>
```

By including this line, you acknowledge your agreement to the DCO 1.1 for that specific contribution.

- Use a valid name and email address—anonymous contributions are not accepted.
- Ensure your email matches the one associated with your GitHub account.

### Commit signature verification

In addition to the sign-off requirement, contributors must also cryptographically sign their commits to verify authenticity. See: [GitHub's documentation on commit signature verification](https://docs.github.com/en/authentication/managing-commit-signature-verification).
