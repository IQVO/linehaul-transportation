Feature: Lane schedule rules and the departures they generate
  DefineLaneSchedule, RetireLaneSchedule and GenerateDepartures (use cases;
  ADR 0002 A1-A6): slot validation, the 14-day horizon, the time zones of
  cutoff and arrival, the kind of the generated departure and what retiring or
  redefining leaves behind. The clock is Friday 2026-10-23 12:00 UTC, which is
  09:00 at the nodes (America/Sao_Paulo, UTC-3).

  Background:
    Given an active sortation center "sc-1"
    And an active delivery station "ds-1"
    And an open GROUND lane "ln-2" from "sc-1" to "ds-1"

  # --- the horizon and the times of a generated departure ---------------------

  Scenario: A weekly slot yields one departure per matching day of the 14-day horizon
    Given the schedule of "ln-2" has a slot on "friday" cutoff "20:00" departs "21:00" arrives "23:00" for 100 packages
    When departures are generated
    Then the lane "ln-2" has 2 LINEHAUL departures
    And the departure "H1" is the one on lane "ln-2" cutting off at "2026-10-23T23:00:00Z"
    And the departure "H1" leaves at "2026-10-24T00:00:00Z" and arrives at "2026-10-24T02:00:00Z"
    And the departure "H1" has capacity 100
    And the lane "ln-2" has a departure cutting off at "2026-10-30T23:00:00Z"
    And the lane "ln-2" has no departure cutting off at "2026-11-06T23:00:00Z"

  Scenario Outline: A slot whose cutoff is not after the current instant is not generated today
    Given the schedule of "ln-2" has a slot on "friday" cutoff "<cutoff>" departs "09:30" arrives "11:00" for 100 packages
    When departures are generated
    Then the lane "ln-2" has <departures> LINEHAUL departures

    Examples:
      | cutoff | departures |
      | 09:00  | 1          |
      | 09:01  | 2          |

  Scenario Outline: The arrival is the first instant after the departure at the arrival time of the destination
    Given the schedule of "ln-2" has a slot on "friday" cutoff "20:00" departs "<departs>" arrives "<arrives>" for 100 packages
    When departures are generated
    Then the departure "H1" is the one on lane "ln-2" cutting off at "2026-10-23T23:00:00Z"
    And the departure "H1" leaves at "<leaves>" and arrives at "<arrival>"

    Examples:
      | departs | arrives | leaves               | arrival              |
      | 21:00   | 23:00   | 2026-10-24T00:00:00Z | 2026-10-24T02:00:00Z |
      | 22:00   | 01:00   | 2026-10-24T01:00:00Z | 2026-10-24T04:00:00Z |
      | 21:00   | 21:00   | 2026-10-24T00:00:00Z | 2026-10-25T00:00:00Z |

  Scenario: The arrival is read in the time zone of the destination node
    Given an active delivery station "ds-2" in zone "America/Manaus"
    And an open GROUND lane "ln-4" from "sc-1" to "ds-2"
    And the schedule of "ln-4" has a slot on "friday" cutoff "20:00" departs "21:00" arrives "23:00" for 100 packages
    When departures are generated
    Then the departure "H1" is the one on lane "ln-4" cutting off at "2026-10-23T23:00:00Z"
    And the departure "H1" leaves at "2026-10-24T00:00:00Z" and arrives at "2026-10-24T03:00:00Z"

  Scenario: A GROUND lane yields LINEHAUL departures and an AIR lane yields AIR ones
    Given an active air hub "ah-1"
    And an open AIR lane "ln-5" from "sc-1" to "ah-1"
    And the schedule of "ln-2" has a slot on "friday" cutoff "20:00" departs "21:00" arrives "23:00" for 100 packages
    And the schedule of "ln-5" has a slot on "friday" cutoff "20:00" departs "21:00" arrives "23:00" for 100 packages
    When departures are generated
    Then the lane "ln-2" has 2 LINEHAUL departures
    And the lane "ln-2" has 0 AIR departures
    And the lane "ln-5" has 2 AIR departures
    And the lane "ln-5" has 0 LINEHAUL departures

  Scenario: A schedule on a lane that leaves a vendor fulfillment center generates no line-haul departure
    Given a vendor fulfillment center "fc-1" with a pickup cutoff at "18:00" on weekdays
    And an open GROUND lane "ln-1" from "fc-1" to "sc-1"
    And the node "fc-1" is activated
    And the schedule of "ln-1" has a slot on "friday" cutoff "20:00" departs "21:00" arrives "23:00" for 100 packages
    When departures are generated
    Then the lane "ln-1" has 0 LINEHAUL departures
    And the lane "ln-1" has 10 PICKUP departures

  Scenario: A lane whose destination zone is not known yet is skipped and retried at the next run
    Given an open GROUND lane "ln-9" from "sc-1" to "ds-9"
    And the schedule of "ln-9" has a slot on "friday" cutoff "20:00" departs "21:00" arrives "23:00" for 100 packages
    When departures are generated
    Then the command succeeds
    And no departure was scheduled by the last command
    When an active delivery station "ds-9"
    And departures are generated
    Then the lane "ln-9" has 2 LINEHAUL departures

  Scenario: A closed lane does not stop the departures of the other lanes
    Given an open GROUND lane "ln-3" from "sc-1" to "ds-1"
    And the schedule of "ln-2" has a slot on "friday" cutoff "20:00" departs "21:00" arrives "23:00" for 100 packages
    And the schedule of "ln-3" has a slot on "friday" cutoff "20:00" departs "21:00" arrives "23:00" for 100 packages
    And the lane "ln-2" is closed
    When departures are generated
    Then the command succeeds
    And the lane "ln-3" has 2 LINEHAUL departures
    And the lane "ln-2" has 0 LINEHAUL departures

  Scenario: A cancelled departure is not resurrected by the next run
    Given the schedule of "ln-2" has a slot on "friday" cutoff "20:00" departs "21:00" arrives "23:00" for 100 packages
    And departures are generated
    And the departure "H1" is the one on lane "ln-2" cutting off at "2026-10-23T23:00:00Z"
    And the departure "H1" is cancelled because "breakdown"
    When departures are generated
    Then no departure was scheduled by the last command
    And the departure "H1" is CANCELLED
    And the lane "ln-2" has 2 LINEHAUL departures

  # --- slot validation (ADR 0002 A3) -------------------------------------------

  Scenario: Slots that merely touch overlap
    When I define the schedule of "ln-2" with the slots:
      | day    | cutoff | departs | arrives | capacity |
      | friday | 10:00  | 12:00   | 14:00   | 10       |
      | friday | 12:00  | 13:00   | 14:00   | 10       |
    Then the command is refused with "overlapping-slots"

  Scenario: A slot inside another slot overlaps it
    When I define the schedule of "ln-2" with the slots:
      | day    | cutoff | departs | arrives | capacity |
      | friday | 10:00  | 12:00   | 14:00   | 10       |
      | friday | 11:00  | 11:30   | 14:00   | 10       |
    Then the command is refused with "overlapping-slots"

  Scenario: Slots a minute apart, or on different weekdays, do not overlap
    When I define the schedule of "ln-2" with the slots:
      | day      | cutoff | departs | arrives | capacity |
      | friday   | 10:00  | 11:59   | 14:00   | 10       |
      | friday   | 12:00  | 13:00   | 14:00   | 10       |
      | saturday | 10:00  | 12:00   | 14:00   | 10       |
    Then the command succeeds

  Scenario: A slot may depart at its own cutoff
    When I define the schedule of "ln-2" with a slot on "friday" cutoff "20:00" departs "20:00" arrives "22:00" for 100 packages
    Then the command succeeds

  Scenario: An overnight slot is refused
    When I define the schedule of "ln-2" with a slot on "friday" cutoff "23:00" departs "01:00" arrives "03:00" for 100 packages
    Then the command is refused with "invalid-departure-times"

  Scenario: A schedule of 100 slots is accepted
    When I define the schedule of "ln-2" with 100 slots on "friday" ten minutes apart
    Then the command succeeds

  Scenario: A schedule of 101 slots is refused
    When I define the schedule of "ln-2" with 101 slots on "friday" ten minutes apart
    Then the command is refused with "validation-failed"

  Scenario: A schedule without slots is refused
    When I define the schedule of "ln-2" with 0 slots on "friday" ten minutes apart
    Then the command is refused with "validation-failed"

  Scenario Outline: A slot capacity from 1 to 2147483647 is accepted
    When I define the schedule of "ln-2" with a slot on "friday" cutoff "20:00" departs "21:00" arrives "23:00" for <capacity> packages
    Then the command succeeds

    Examples:
      | capacity   |
      | 1          |
      | 2147483647 |

  Scenario Outline: A slot capacity outside 1 to 2147483647 is refused
    When I define the schedule of "ln-2" with a slot on "friday" cutoff "20:00" departs "21:00" arrives "23:00" for <capacity> packages
    Then the command is refused with "validation-failed"

    Examples:
      | capacity   |
      | 0          |
      | 2147483648 |

  Scenario Outline: A slot with an unknown weekday or a time that is not HH:mm is refused
    When I define the schedule of "ln-2" with a slot on "<day>" cutoff "<cutoff>" departs "21:00" arrives "23:00" for 100 packages
    Then the command is refused with "validation-failed"

    Examples:
      | day    | cutoff |
      | funday | 20:00  |
      | friday | 25:00  |
      | friday | 20h00  |

  # --- redefining, retiring, closed lanes (ADR 0002 A4) --------------------------

  Scenario: Redefining a schedule leaves the departures already generated
    Given the schedule of "ln-2" has a slot on "friday" cutoff "20:00" departs "21:00" arrives "23:00" for 100 packages
    And departures are generated
    When I define the schedule of "ln-2" with a slot on "saturday" cutoff "10:00" departs "11:00" arrives "13:00" for 50 packages
    And departures are generated
    Then the command succeeds
    And the lane "ln-2" has a LINEHAUL departure with capacity 100
    And the lane "ln-2" has a LINEHAUL departure with capacity 50

  Scenario: Retiring a schedule cancels nothing
    Given the schedule of "ln-2" has a slot on "friday" cutoff "20:00" departs "21:00" arrives "23:00" for 100 packages
    And departures are generated
    And the departure "H1" is the one on lane "ln-2" cutting off at "2026-10-23T23:00:00Z"
    When the schedule of "ln-2" is retired
    Then the command succeeds
    And the departure "H1" is SCHEDULED
    And the lane "ln-2" has 2 LINEHAUL departures

  Scenario: Retiring a retired schedule changes nothing
    Given the schedule of "ln-2" has a slot on "friday" cutoff "20:00" departs "21:00" arrives "23:00" for 100 packages
    And the schedule of "ln-2" is retired
    When the schedule of "ln-2" is retired
    Then the command succeeds

  Scenario: A lane without a schedule cannot be retired
    When the schedule of "ln-2" is retired
    Then the command is refused with "not-found"

  Scenario: A closed lane may be given a schedule but gets no departure from it
    Given the lane "ln-2" is closed
    When I define the schedule of "ln-2" with a slot on "friday" cutoff "20:00" departs "21:00" arrives "23:00" for 100 packages
    Then the command succeeds
    When departures are generated
    Then no departure was scheduled by the last command
