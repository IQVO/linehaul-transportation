Feature: Booking a locked assignment on its pickup departure
  FulfillmentLocked books one package on the pickup departure the assignment
  was planned on. When that departure cannot hold it the booking moves to the
  next departure from the same origin (BookingMoved), or fails (BookingFailed)
  when none fits. The node never changes after lock.

  Background:
    Given a vendor fulfillment center "fc-1" with a pickup cutoff at "18:00" on weekdays
    And an active sortation center "sc-1"
    And an open GROUND lane "ln-1" from "fc-1" to "sc-1"
    And the node "fc-1" is activated
    And the pickup departure "P1" is the 1st from "fc-1"
    And the pickup departure "P2" is the 2nd from "fc-1"

  Scenario: A lock books one package on the planned departure
    When the assignment "as-1" is locked on "P1"
    Then the command succeeds
    And the departure "P1" holds 1 booked package
    And the assignment "as-1" is booked on "P1"
    And the last command published the event "DepartureCapacityChanged"

  Scenario: A repeated lock of the same assignment books nothing twice
    Given the assignment "as-1" is locked on "P1"
    When the lock of "as-1" on "P1" is delivered again as a new event
    Then the departure "P1" holds 1 booked package

  Scenario: A full departure moves the booking to the next one
    Given the capacity of "P1" is changed to 1
    And the assignment "as-1" is locked on "P1"
    When the assignment "as-2" is locked on "P1"
    Then the assignment "as-2" is booked on "P2"
    And the last command published the event "BookingMoved"
    And the departure "P1" holds 1 booked package

  Scenario: No departure with room fails the booking
    Given the capacity of every pickup departure of "fc-1" is changed to 0
    When the assignment "as-1" is locked on "P1"
    Then the last command published the event "BookingFailed"
    And the assignment "as-1" is in state "FAILED"

  Scenario: Cancelling a departure rebooks what it holds
    Given the assignment "as-1" is locked on "P1"
    When the departure "P1" is cancelled because "weather"
    Then the command succeeds
    And the assignment "as-1" is booked on "P2"
    And the last command published the event "BookingMoved"
    And the departure "P1" is CANCELLED

  Scenario: Cancelling a cancelled departure changes nothing
    Given the departure "P1" is cancelled because "weather"
    When the departure "P1" is cancelled because "again"
    Then the command succeeds
    And the departure "P1" is CANCELLED

  Scenario: Capacity cannot drop below what is booked
    Given the assignment "as-1" is locked on "P1"
    When the capacity of "P1" is changed to 0
    Then the command is refused with "departure-full"
    And the departure "P1" holds 1 booked package

  Scenario: An unknown departure is not found
    When the departure "nope" is cancelled because "weather"
    Then the command is refused with "not-found"
