Feature: The assignment projection of order-sourcing and shipping facts
  FulfillmentReleased, FulfillmentReassigned and FulfillmentCancelled keep
  the expected volume of an assignment current; LabelIssued and LabelVoided
  track its labels. They are view updates only: none of them publishes an
  event, and a booking already made stays in place (ADR 0002 A15-A18).

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

  Scenario: A release records the node and the planned pickup departure
    When the assignment "as-1" is released to node "fc-1" on the pickup departure "P1" as release 1
    Then the command succeeds
    And the assignment "as-1" is in state "RELEASED"
    And the assignment "as-1" is at node "fc-1"
    And the assignment "as-1" is planned on the pickup departure "P1"
    And no event was published by the last command

  Scenario: An older release number is ignored
    Given the assignment "as-1" is released to node "fc-1" on the pickup departure "P1" as release 2
    When the assignment "as-1" is released to node "fc-2" on the pickup departure "P9" as release 1
    Then the command succeeds
    And the assignment "as-1" is in state "RELEASED"
    And the assignment "as-1" is planned on the pickup departure "P1"
    And no event was published by the last command

  Scenario: A reassignment moves the node and forgets the planned departure
    Given the assignment "as-1" is released to node "fc-1" on the pickup departure "P1" as release 1
    When the assignment "as-1" is reassigned to node "fc-2"
    Then the command succeeds
    And the assignment "as-1" is at node "fc-2"
    And the assignment "as-1" has no planned departure
    And no event was published by the last command

  Scenario: An assignment that was never released is not reassigned
    When the assignment "as-1" is reassigned to node "fc-2"
    Then the command succeeds
    And no event was published by the last command

  Scenario: Cancelling a booked assignment keeps its booking
    Given the assignment "as-1" is locked on "P1"
    When the assignment "as-1" is cancelled by order-sourcing
    Then the command succeeds
    And the assignment "as-1" is in state "CANCELLED"
    And the assignment "as-1" still holds its booking on "P1"
    And the departure "P1" holds 1 booked package
    And no event was published by the last command

  Scenario: A repeated cancellation changes nothing
    Given the assignment "as-1" is cancelled by order-sourcing
    When the assignment "as-1" is cancelled by order-sourcing
    Then the command succeeds
    And no event was published by the last command

  Scenario: A release number out of range is refused
    When the assignment "as-1" is released to node "fc-1" on the pickup departure "P1" as release 0
    Then the command is refused with "validation-failed"

  Scenario: A reassignment to an invalid node is refused
    Given the assignment "as-1" is released to node "fc-1" on the pickup departure "P1" as release 1
    When the assignment "as-1" is reassigned to node "not a valid id"
    Then the command is refused with "validation-failed"

  Scenario: An issued label is tracked on the assignment
    Given the assignment "as-1" is locked on "P1"
    When a label with tracking number "TRK-1" is issued for the assignment "as-1"
    Then the command succeeds
    And the assignment "as-1" tracks the label "TRK-1"
    And no event was published by the last command

  Scenario: The same label issued twice is tracked once
    Given a label with tracking number "TRK-1" is issued for the assignment "as-1"
    When a label with tracking number "TRK-1" is issued for the assignment "as-1"
    Then the command succeeds
    And the assignment "as-1" tracks the label "TRK-1"

  Scenario: Voiding a label removes it, the booking stays
    Given the assignment "as-1" is locked on "P1"
    And a label with tracking number "TRK-1" is issued for the assignment "as-1"
    When the label with tracking number "TRK-1" is voided
    Then the command succeeds
    And the assignment "as-1" tracks no labels
    And the assignment "as-1" is booked on "P1"
    And no event was published by the last command

  Scenario: Voiding a label no assignment tracks changes nothing
    When the label with tracking number "TRK-unknown" is voided
    Then the command succeeds
    And no event was published by the last command

  Scenario: A label with an invalid tracking number is refused
    When a label with tracking number "" is issued for the assignment "as-1"
    Then the command is refused with "validation-failed"
