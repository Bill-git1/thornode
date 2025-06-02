<!-- markdownlint-disable MD024 -->

# Versioning

THORNode follows a custom variation of semantic version structure: `MAJOR.MINOR.PATCH` (e.g. `3.7.0`).

- The MAJOR version currently is updated per genesis export soft-fork.
- The MINOR version is updated for each consensus breaking release.
- The PATCH version is updated for each release and generally does not break consensus - Bifrost-only patches, API fixes, etc. In some specific cases (on `stagenet` for release candidates) the PATCH version may be used to indicate a consensus breaking change.

## Release Preparation

1. Create a milestone using the release version (e.g. [Release-3.7.0](https://gitlab.com/thorchain/thornode/-/milestones/161)).
2. Tag issues and PRs using the milestone, so we can identify which PR is on which version.
3. PRs require 3 approvals from the dev team - once approved, merge to `develop` branch.

## Stagenet Release

1. Create a new RC release at https://gitlab.com/thorchain/thornode/-/releases for the version (e.g. `v3.7.0-rc1`) with the last commit for the release in `develop` as the tag target. Run `./scripts/prlog.py Release-3.7.0` to generate the output for the change log.
2. Checkout the release commit locally, then create the `stagenet` branch from it, and push it to the remote (force push may be necessary):

   ```bash
   git checkout v3.7.0-rc1
   git branch -D stagenet
   git checkout -b stagenet
   git push -f origin stagenet
   ```

3. The `build-thornode` job in the pipeline for the `stagenet` branch will build the release image. The upstream `node-launcher` values for `stagenet` images do not need to be kept up-to-date - they are for reference and state can be kept locally since there may be multiple private `stagenet` deployments.
4. Ensure authors of any large changes or features have provided a test plan.
5. Send the upgrade proposal from one of the active validators (`make shell` in `node-launcher`):

   ```bash
   thornode tx thorchain propose-upgrade [version] [height] --node http://localhost:27147 --chain-id thorchain-stagenet-2 --keyring-backend file --from thorchain
   ```

6. Approve the upgrade from all other nodes via the `make upgrade-vote` target in `node-launcher`.
7. Post in Discord `#stagenet` channel once the upgrade has succeeded tagging `@here` and requesting code authors to test their changes.
8. Testing will vary depending on the contents of the release, but general process should include:
   - Assist authors of large features and changes in completion of their test plan.
   - Ensure `stagenet` successfully completes a churn.
   - Operators of `stagenet` should test any node or operator specific functionality changes.
   - UIs should be sanity checked against any new API changes.
9. If there are bugs requiring a re-cut of the release, create a new release candidate (e.g. `v3.7.0-rc2`) and repeat the process from step 1, but add an additional commit directly on the `stagenet` branch before pushing that includes a patch release version bump ([example](https://gitlab.com/thorchain/thornode/-/commit/aea4146d05f9a94fbaf3105205f536a6b5b77a14)). Since the embedded version cannot support the RC suffix, subsequent RCs on `stagenet` will correspond to a patch version bump on `stagenet` only (e.g. `3.7.1` for `v3.7.0-rc2`).
10. Once `stagenet` has baked for the agreed period with no further issues, proceed with the `mainnet` release process.

## Mainnet Release

1. Determine the upgrade height and time - this must be at least 1 week out to accommodate agreements with exchanges and other stakeholders.
2. Copy the contents of the final RC release (e.g. `v3.7.0-rc1`) to a new release (e.g. `v3.7.0`) at https://gitlab.com/thorchain/thornode/-/releases, also including the upgrade height and time in the following format:

   ```text
   - **Proposed Block**: `21210000`
   - **Date**: 22-May-2025 @ ~1:00pm EDT - https://runescan.io/block/21jj210000
   - **Note**: Block time is presently fluctuating - this time is an estimate, but may fluctuate by hours.

   **Changelog**
   ...
   ```

3. Checkout the release commit locally, then create the `mainnet` branch from it, and push it to the remote (force push may be necessary):

   ```bash
   git checkout v3.7.0-rc1
   git branch -D mainnet
   git checkout -b mainnet
   git push -f origin mainnet
   ```

4. The `build-thornode` job in the pipeline for the `mainnet` branch will build the release image.

5. Send the upgrade proposal from one of the active validators (`make shell` in `node-launcher`):

   ```bash
   thornode tx thorchain propose-upgrade [version] [height] --node http://localhost:27147 --chain-id thorchain-1 --keyring-backend file --from thorchain
   ```

6. Announce the upgrade proposal to be voted on in the Discord `#thornode-mainnet` channel with the format:

   ```text
   ### Mainnet 3.6.0 Upgrade Proposal - Validators Only

   **Changelog**: https://gitlab.com/thorchain/thornode/-/releases/v3.6.0
   **Block**: `21210000`
   **Date**: 22-May-2025 @ ~1:00pm EDT - https://runescan.io/block/21210000
   **Note**: Block time is presently fluctuating - this time is an estimate, but may fluctuate by hours.

   <short description here>

   Please approve via `make upgrade-vote`:

   Select: `mainnet`
   Enter THORNode name: <thornode namespace>
   Enter THORNode upgrade proposal name: `3.6.0`
   Select THORNode upgrade proposal vote: `yes`

   @everyone
   ```

7. Relay the announcement to the Telegram channel and any relevant chats with exchanges to notify them of the upcoming fork.

8. PR in `node-launcher` to extend the [`thornode.versions`](https://gitlab.com/thorchain/devops/node-launcher/-/blob/master/thornode-stack/mainnet.yaml?ref_type=heads#L5) with the upgrade height and new version image.

9. Send an announcement in Discord `#thornode-mainnet` channel instructing nodes to apply so the upgrade is configured and the Cosmos Operator will make the switch at the upgrade height. The announcement can follow the format:

   ````text
   ## THORNODE ❗️MAINNET❗️UPDATE 3.7.0
   https://gitlab.com/thorchain/thornode/-/releases/v3.7.0

   NETWORK: MAINNET
   TYPE: Non-coordianted
   URGENCY: ASAP

   The upgrade proposal has passed. Please install to schedule the upgrade at the proposed height. Nodes will automatically update to the new image at the scheduled height, and the node version for active validators will be automatically set at that time. All non-active validators must run `make set-version` after the upgrade height to be eligible for the subsequent churn.

   ```
   make pull
   make install
   ```

   @everyone
   ````
