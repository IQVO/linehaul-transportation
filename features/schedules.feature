Feature: Lane schedules and line-haul departures
  A lane schedule is a set of weekly slots (day, local cutoff, departs,
  arrives, capacity). Generating departures expands the active schedules over
  the 14-day horizon, once.

  Background:
    Given an active sortation center "sc-1"
    And an active delivery station "ds-1"
    And an open GROUND lane "ln-2" from "sc-1" to "ds-1"

  Scenario: Defining a schedule and generating its departures
    When I define the schedule of "ln-2" with a slot on "friday" cutoff "20:00" departs "21:00" arrives "23:00" for 100 packages
    And departures are generated
    Then the command succeeds
    And the lane "ln-2" has LINEHAUL departures with capacity 100
    And the last command published the event "DepartureScheduled"

  Scenario: A second generation schedules nothing new
    Given the schedule of "ln-2" has a slot on "friday" cutoff "20:00" departs "21:00" arrives "23:00" for 100 packages
    And departures are generated
    When departures are generated
    Then no departure was scheduled by the last command

  Scenario: Overlapping slots are refused
    When I define the schedule of "ln-2" with two slots on "friday" both cutoff "20:00" departs "21:00" arrives "23:00" for 50 packages
    Then the command is refused with "overlapping-slots"

  Scenario: A slot whose times are not in order is refused
    When I define the schedule of "ln-2" with a slot on "friday" cutoff "22:00" departs "21:00" arrives "23:00" for 100 packages
    Then the command is refused with "invalid-departure-times"

  Scenario: A schedule needs a known lane
    When I define the schedule of "ln-unknown" with a slot on "friday" cutoff "20:00" departs "21:00" arrives "23:00" for 100 packages
    Then the command is refused with "not-found"

  Scenario: A retired schedule generates nothing
    Given the schedule of "ln-2" has a slot on "friday" cutoff "20:00" departs "21:00" arrives "23:00" for 100 packages
    And the schedule of "ln-2" is retired
    When departures are generated
    Then no departure was scheduled by the last command

  Scenario: A closed lane schedules no new departure
    Given the schedule of "ln-2" has a slot on "friday" cutoff "20:00" departs "21:00" arrives "23:00" for 100 packages
    And the lane "ln-2" is closed
    When departures are generated
    Then no departure was scheduled by the last command
