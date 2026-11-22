Feature: Out-of-order and repeated facts of the assignment projection
  order-sourcing and shipping-and-tracking facts can arrive late, twice or in
  the wrong order. The rules are in the consumed-events table of the domain
  events page and ADR 0002 A13, A16-A18: the lock books once, the node never
  changes after the lock, a cancelled assignment books nothing and a label
  may come before the assignment it belongs to.

  Background:
    Given a vendor fulfillment center "fc-1" with a pickup cutoff at "18:00" on weekdays
    And a vendor fulfillment center "fc-2" with a pickup cutoff at "18:00" on weekdays
    And an active sortation center "sc-1"
    And an open GROUND lane "ln-1" from "fc-1" to "sc-1"
    And an open GROUND lane "ln-2" from "fc-2" to "sc-1"
    And the node "fc-1" is activated
    And the node "fc-2" is activated
    And the pickup departure "P1" is the 1st from "fc-1"
    And the pickup departure "P9" is the 1st from "fc-2"

  Scenario: A lock for an assignment already cancelled books nothing
    Given the assignment "as-1" is cancelled by order-sourcing
    When the assignment "as-1" is locked on "P1"
    Then the command succeeds
    And the departure "P1" holds 0 booked packages
    And the assignment "as-1" is in state "CANCELLED"
    And no event was published by the last command

  Scenario: A release that arrives after the lock never touches the booking
    Given the assignment "as-1" is locked on "P1"
    When the assignment "as-1" is released to node "fc-2" on the pickup departure "P9" as release 1
    Then the command succeeds
    And the assignment "as-1" is booked on "P1"
    And the assignment "as-1" is at node "fc-1"
    And the departure "P1" holds 1 booked package
    And no event was published by the last command

  Scenario: A release that arrives after the cancellation is ignored
    Given the assignment "as-1" is cancelled by order-sourcing
    When the assignment "as-1" is released to node "fc-1" on the pickup departure "P1" as release 1
    Then the command succeeds
    And the assignment "as-1" is in state "CANCELLED"
    And the assignment "as-1" has no planned departure

  Scenario: A reassignment after the lock does not move the node
    Given the assignment "as-1" is locked on "P1"
    When the assignment "as-1" is reassigned to node "fc-2"
    Then the command succeeds
    And the assignment "as-1" is at node "fc-1"
    And the assignment "as-1" is booked on "P1"
    And no event was published by the last command

  Scenario: A release with the same release number as the stored one replaces the facts
    Given the assignment "as-1" is released to node "fc-1" on the pickup departure "P1" as release 2
    When the assignment "as-1" is released to node "fc-2" on the pickup departure "P9" as release 2
    Then the command succeeds
    And the assignment "as-1" is at node "fc-2"
    And the assignment "as-1" is planned on the pickup departure "P9"

  Scenario: A label that arrives before its assignment is kept when the release arrives
    When a label with tracking number "TRK-9" is issued for the assignment "as-7"
    Then the command succeeds
    And the assignment "as-7" is in state "UNKNOWN"
    And the assignment "as-7" tracks the label "TRK-9"
    When the assignment "as-7" is released to node "fc-1" on the pickup departure "P1" as release 1
    Then the assignment "as-7" is in state "RELEASED"
    And the assignment "as-7" tracks the label "TRK-9"

  Scenario: The same lock event delivered twice books once
    When the lock of "as-1" on "P1" is delivered twice with the same event id
    Then the command succeeds
    And the departure "P1" holds 1 booked package
    And no event was published by the last command

  Scenario: A follower lock that arrives after the carrier's lock books nothing more
    Given the assignment "as-1" is locked on "P1"
    When the assignment "as-2" merged into "as-1" is locked on "P1"
    Then the command succeeds
    And the departure "P1" holds 1 booked package
    And the assignment "as-2" has no volume row
    And no event was published by the last command

  Scenario: An assignment id of 64 characters is accepted
    When a label with tracking number "TRK-9" is issued for an assignment whose id has 64 characters
    Then the command succeeds

  Scenario: An assignment id of 65 characters is refused
    When a label with tracking number "TRK-9" is issued for an assignment whose id has 65 characters
    Then the command is refused with "validation-failed"
